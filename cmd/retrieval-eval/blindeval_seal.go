package main

// The seal phase: turn raw rater, grader and adjudicator output into
// content-addressed records, and build the grader's packets.
//
// Raters and graders write plain text. They cannot compute a content address
// over a record that does not exist yet, and letting them try would put the
// ordering evidence in the hands of the party it constrains. So sealing is a
// separate, mechanical step:
//
//   - a raw response file becomes a RaterResponse whose responded_at is the
//     file's own modification time and whose status is derived from its
//     contents by rule — absent is `missing`, blank is `empty`, a file opening
//     with the refusal marker is `refused`, anything else is `answered`;
//   - a sealed answered response becomes a grader packet carrying the question,
//     the exact bundle bytes, the response, and the reviewed grade-3 answer
//     spans;
//   - a raw grade file becomes a Grade bound to that response's content
//     address.
//
// Sealing is idempotent AND append-only: the same raw material seals to the
// same bytes, because every field it derives comes from the raw file rather
// than from the clock — and DIFFERENT material for an address that is already
// sealed is refused. Overwriting was the one supported retry loop this
// evaluation had: change a raw FAIL to PASS, re-run seal, re-run decide, and
// the old grade was gone with the replacement validly sealed.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/samibel/graphi/internal/eval/retrieval"
)

// Raw-material directories, written by the raters, graders and adjudicator.
const (
	blindEvalRawResponsesDir     = "responses-raw"
	blindEvalRawGradesDir        = "grades-raw"
	blindEvalRawAdjudicationsDir = "adjudications-raw"
	blindEvalGraderPacketsDir    = "grader-packets"
)

// rawResponseFileName is where a rater writes its plain-text answer. It mirrors
// the sealed record's name so the two are obviously paired.
func rawResponseFileName(queryID, raterID string) string {
	return strings.TrimSuffix(retrieval.ResponseFileName(queryID, raterID), ".json") + ".txt"
}

// rawGradeFileName is where a grader reads its packet and writes its verdict.
func rawGradeFileName(queryID, raterID string) string {
	return strings.TrimSuffix(retrieval.ResponseFileName(queryID, raterID), ".json") + ".txt"
}

// blindEvalRefusalMarker is the one spelling a rater uses to decline. It is
// distinct from INSUFFICIENT, which is a substantive answer and is graded.
const blindEvalRefusalMarker = "REFUSED"

func runBlindEvalSeal(o blindEvalOptions, stdout, stderr io.Writer) int {
	if o.dir == "" {
		fmt.Fprintln(stderr, "retrieval-eval: -blind-eval seal needs -blind-eval-dir")
		return exitUsage
	}
	precondition, err := retrieval.LoadPreconditionRecord(filepath.Join(o.dir, retrieval.BlindEvalPreconditionFile))
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	pre, err := retrieval.LoadPreRegistration(filepath.Join(o.dir, retrieval.BlindEvalPreRegFile))
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	dataset, err := retrieval.LoadDataset(filepath.Join(o.root, filepath.FromSlash(precondition.DatasetPath)))
	if err != nil {
		fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
		return exitError
	}
	if dataset.SHA256 != precondition.DatasetSHA256 {
		fmt.Fprintf(stderr, "retrieval-eval: dataset sha256 %s, precondition froze %s\n", dataset.SHA256, precondition.DatasetSHA256)
		return exitError
	}
	rubricSHA := ""
	for _, input := range precondition.Inputs {
		if input.Role == "grading_rubric" {
			rubricSHA = input.SHA256
		}
	}
	queries := map[string]retrieval.Query{}
	for _, q := range dataset.Dataset.Queries {
		queries[q.ID] = q
	}

	sealedResponses := map[string]retrieval.RaterResponse{}
	adjudicatorResponses := map[string]retrieval.RaterResponse{}
	counts := map[string]int{}
	for _, prq := range pre.Queries {
		for _, rater := range pre.PrimaryRaters {
			response, err := sealOneResponse(o.dir, pre, prq, rater, retrieval.RaterRolePrimary,
				filepath.Join(o.dir, blindEvalRawResponsesDir, rawResponseFileName(prq.QueryID, rater.ID)))
			if err != nil {
				fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
				return exitError
			}
			if err := retrieval.WriteBlindEvalJSONWriteOnce("response", filepath.Join(o.dir, retrieval.BlindEvalResponsesDir,
				retrieval.ResponseFileName(prq.QueryID, rater.ID)), response); err != nil {
				fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
				return exitError
			}
			sealedResponses[response.SHA256] = response
			counts[response.Status]++
		}
		// An adjudicator answer, when one was produced for this query.
		adjRaw := filepath.Join(o.dir, blindEvalRawAdjudicationsDir, prq.QueryID+".txt")
		present, err := rawFilePresent(adjRaw)
		if err != nil {
			fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
			return exitError
		}
		if present {
			response, err := sealOneResponse(o.dir, pre, prq, pre.Adjudicator, retrieval.RaterRoleAdjudicator, adjRaw)
			if err != nil {
				fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
				return exitError
			}
			sealedResponses[response.SHA256] = response
			adjudicatorResponses[prq.QueryID] = response
			counts["adjudicator_"+response.Status]++
		}
	}

	// Grader packets for every answered response, and grades for every raw
	// grade file that names a sealed response.
	//
	// Packets and raw grades are named by the rater SLOT (query--rater), not by
	// the response digest, so a human dispatching the grading cannot transcribe
	// a 64-character hex name wrongly. The binding to the response's content
	// address is not weakened by that: the packet states the address, and the
	// sealed grade below is built from the response object itself.
	packets, grades := 0, 0
	var sealedGrades []retrieval.Grade
	for _, response := range sealedResponses {
		if response.Status != retrieval.ResponseStatusAnswered {
			continue
		}
		slot := rawGradeFileName(response.QueryID, response.RaterID)
		q := queries[response.QueryID]
		bundle, err := loadCapturedBundle(o.dir, response.QueryID)
		if err != nil {
			fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
			return exitError
		}
		packet := buildGraderPacket(q, bundle, response)
		if err := os.MkdirAll(filepath.Join(o.dir, blindEvalGraderPacketsDir), 0o755); err != nil {
			fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
			return exitError
		}
		if err := os.WriteFile(filepath.Join(o.dir, blindEvalGraderPacketsDir, slot), []byte(packet), 0o644); err != nil {
			fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
			return exitError
		}
		packets++

		rawGrade := filepath.Join(o.dir, blindEvalRawGradesDir, slot)
		info, err := os.Stat(rawGrade)
		if err != nil {
			// Only "the grader has not written this yet" is an absence. A
			// permission or I/O failure used to read as a skipped grade, which
			// turns an unreadable run directory into a plausible-looking
			// RELEASE: NO — a wrong answer that looks like the conservative one.
			if os.IsNotExist(err) {
				continue
			}
			fmt.Fprintf(stderr, "retrieval-eval: raw grade %s could not be read: %v\n", rawGrade, err)
			return exitError
		}
		grade, err := sealOneGrade(rawGrade, info, pre.Grader, response, rubricSHA)
		if err != nil {
			fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
			return exitError
		}
		if err := retrieval.WriteBlindEvalJSONWriteOnce("grade", filepath.Join(o.dir, retrieval.BlindEvalGradesDir,
			retrieval.GradeFileName(response.SHA256)), grade); err != nil {
			fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
			return exitError
		}
		sealedGrades = append(sealedGrades, grade)
		grades++
	}

	// Adjudications are built LAST, because a disclosure must name the grades
	// it discloses and the grades do not exist until the loop above has run.
	adjudications := 0
	for _, prq := range pre.Queries {
		response, adjudicated := adjudicatorResponses[prq.QueryID]
		if !adjudicated {
			continue
		}
		adjudication, err := buildAdjudication(o.dir, prq.QueryID, response, sealedGrades)
		if err != nil {
			fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
			return exitError
		}
		if err := retrieval.WriteSealedAdjudication(filepath.Join(o.dir, retrieval.BlindEvalAdjudicationsDir,
			retrieval.BundleFileName(prq.QueryID)), adjudication); err != nil {
			fmt.Fprintf(stderr, "retrieval-eval: %v\n", err)
			return exitError
		}
		adjudications++
	}
	fmt.Fprintf(stdout, "retrieval-eval: sealed %d responses (%v), wrote %d grader packets, sealed %d grades and %d adjudications\n",
		len(sealedResponses), counts, packets, grades, adjudications)
	return exitOK
}

// rawFilePresent distinguishes "the rater wrote nothing here" from "this file
// could not be examined". Only os.IsNotExist is an absence; anything else is
// an error, because a permission failure that reads as an absent adjudication
// silently removes a query's third opinion.
func rawFilePresent(path string) (bool, error) {
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return true, nil
	case os.IsNotExist(err):
		return false, nil
	default:
		return false, fmt.Errorf("raw file %s could not be examined: %w", path, err)
	}
}

// sealOneResponse derives one response record from its raw file. Status is
// derived by rule, never chosen: an absent file is a missing response, and a
// missing response is a failure that cannot be adjudicated away.
func sealOneResponse(dir string, pre retrieval.PreRegistration, prq retrieval.PreRegisteredQuery, who retrieval.Participant, role, rawPath string) (retrieval.RaterResponse, error) {
	promptPath := filepath.Join(dir, retrieval.BlindEvalPromptsDir, strings.TrimSuffix(retrieval.BundleFileName(prq.QueryID), ".json")+".txt")
	promptBytes, err := os.ReadFile(promptPath)
	if err != nil {
		return retrieval.RaterResponse{}, fmt.Errorf("read the prompt the rater was given (%s): %w", promptPath, err)
	}
	response := retrieval.RaterResponse{
		ContractVersion:       retrieval.QrelBlindSmokeContractVersion,
		Evaluation:            retrieval.QrelBlindSmokeEvaluationName,
		Role:                  role,
		QueryID:               prq.QueryID,
		RaterID:               who.ID,
		Provider:              who.Provider,
		Model:                 who.Model,
		PreRegistrationSHA256: pre.SHA256,
		QueryTextSHA256:       prq.QueryTextSHA256,
		BundleSHA256:          prq.BundleSHA256,
		PromptSHA256:          retrieval.SHA256Hex(promptBytes),
		Inputs:                []string{"answer_instructions", "query_text", "preserved_bundle"},
	}
	info, statErr := os.Stat(rawPath)
	if statErr != nil {
		if !os.IsNotExist(statErr) {
			return retrieval.RaterResponse{}, fmt.Errorf("raw response %s could not be examined: %w", rawPath, statErr)
		}
		response.Status = retrieval.ResponseStatusMissing
		response.RespondedAt = pre.RecordedAt
		return retrieval.SealRaterResponse(response)
	}
	raw, err := os.ReadFile(rawPath)
	if err != nil {
		return retrieval.RaterResponse{}, fmt.Errorf("read raw response %s: %w", rawPath, err)
	}
	text := strings.TrimRight(string(raw), "\n")
	response.RespondedAt = info.ModTime().UTC().Truncate(time.Second).Format(time.RFC3339)
	switch {
	case strings.TrimSpace(text) == "":
		response.Status = retrieval.ResponseStatusEmpty
		response.Text = ""
	case strings.HasPrefix(strings.TrimSpace(text), blindEvalRefusalMarker):
		response.Status = retrieval.ResponseStatusRefused
		response.Text = text
	default:
		response.Status = retrieval.ResponseStatusAnswered
		response.Text = text
	}
	return retrieval.SealRaterResponse(response)
}

func sealOneGrade(rawPath string, info os.FileInfo, grader retrieval.Participant, response retrieval.RaterResponse, rubricSHA string) (retrieval.Grade, error) {
	raw, err := os.ReadFile(rawPath)
	if err != nil {
		return retrieval.Grade{}, fmt.Errorf("read raw grade %s: %w", rawPath, err)
	}
	text := strings.TrimSpace(string(raw))
	var outcome string
	switch {
	case strings.HasPrefix(text, "PASS"):
		outcome = retrieval.GradeOutcomePass
	case strings.HasPrefix(text, "FAIL"):
		outcome = retrieval.GradeOutcomeFail
	default:
		return retrieval.Grade{}, fmt.Errorf("raw grade %s does not begin with PASS or FAIL; a grade that cannot be read is not a grade", rawPath)
	}
	rationale := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(text, "PASS"), "FAIL"))
	rationale = strings.TrimSpace(strings.TrimPrefix(rationale, ":"))
	if rationale == "" {
		return retrieval.Grade{}, fmt.Errorf("raw grade %s carries no rationale", rawPath)
	}
	grade := retrieval.Grade{
		ContractVersion: retrieval.QrelBlindSmokeContractVersion,
		Evaluation:      retrieval.QrelBlindSmokeEvaluationName,
		QueryID:         response.QueryID,
		ResponseSHA256:  response.SHA256,
		GraderID:        grader.ID,
		Provider:        grader.Provider,
		Model:           grader.Model,
		RubricSHA256:    rubricSHA,
		Outcome:         outcome,
		Rationale:       rationale,
		GradedAt:        info.ModTime().UTC().Truncate(time.Second).Format(time.RFC3339),
	}
	return retrieval.SealGrade(grade)
}

// buildAdjudication pairs the frozen adjudicator response with the disclosure
// record. The adjudicator is never shown a primary response at all; the
// disclosure record marks the point at which the primaries entered the same
// decision, and it names the already-frozen adjudicator response, which is the
// evidence that the response existed first.
func buildAdjudication(dir, queryID string, response retrieval.RaterResponse, sealedGrades []retrieval.Grade) (retrieval.Adjudication, error) {
	var disclosed []string
	entries, err := os.ReadDir(filepath.Join(dir, retrieval.BlindEvalResponsesDir))
	if err != nil {
		return retrieval.Adjudication{}, err
	}
	primaries := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), queryID+"--") {
			continue
		}
		var primary retrieval.RaterResponse
		raw, err := os.ReadFile(filepath.Join(dir, retrieval.BlindEvalResponsesDir, entry.Name()))
		if err != nil {
			return retrieval.Adjudication{}, err
		}
		if err := jsonUnmarshalStrict(raw, &primary); err != nil {
			return retrieval.Adjudication{}, err
		}
		if primary.Role == retrieval.RaterRolePrimary {
			disclosed = append(disclosed, primary.SHA256)
			primaries[primary.SHA256] = true
		}
	}
	// A disagreement is a disagreement between GRADES, so the grades are what
	// entered the adjudicated decision and the disclosure names them too.
	for _, g := range sealedGrades {
		if primaries[g.ResponseSHA256] {
			disclosed = append(disclosed, g.SHA256)
		}
	}
	sort.Strings(disclosed)
	return retrieval.Adjudication{
		QueryID:  queryID,
		Response: response,
		Disclosure: retrieval.DisclosureRecord{
			QueryID:                   queryID,
			AdjudicatorResponseSHA256: response.SHA256,
			DisclosedArtifactSHA256:   disclosed,
			OrderingEvidence:          retrieval.DisclosureOrderingEvidence,
			Limitation:                retrieval.DisclosureLimitation,
		},
	}, nil
}

func loadCapturedBundle(dir, queryID string) (retrieval.CapturedCandidateBundle, error) {
	var bundle retrieval.CapturedCandidateBundle
	raw, err := os.ReadFile(filepath.Join(dir, retrieval.BlindEvalBundlesDir, retrieval.BundleFileName(queryID)))
	if err != nil {
		return bundle, fmt.Errorf("read captured bundle for %s: %w", queryID, err)
	}
	if err := jsonUnmarshalStrict(raw, &bundle); err != nil {
		return bundle, fmt.Errorf("parse captured bundle for %s: %w", queryID, err)
	}
	return bundle, nil
}

// buildGraderPacket assembles the grader's complete input. It carries the
// reviewed grade-3 answer spans, which is the one input the raters never see:
// the raters are what is being measured, the grader is the instrument reading
// their answers, and grading correctness without the key would measure the
// grader's own knowledge of the repository instead.
func buildGraderPacket(q retrieval.Query, bundle retrieval.CapturedCandidateBundle, response retrieval.RaterResponse) string {
	var b strings.Builder
	b.WriteString("You are grading ONE response in a qrel-blind smoke evaluation, against the frozen\n")
	b.WriteString("rubric at docs/eval/retrieval/runs/2026-09-05-sw280-qrel-blind-smoke/grading-rubric.md.\n")
	b.WriteString("Read that rubric, then apply it to the material below. Do not re-answer the question\n")
	b.WriteString("yourself and do not consult any other file, repository or response.\n\n")
	b.WriteString("RESPONSE CONTENT ADDRESS: " + response.SHA256 + "\n")
	b.WriteString("QUERY ID: " + q.ID + "\n\n")
	b.WriteString("QUESTION:\n" + q.Text + "\n\n")
	b.WriteString("REVIEWED GRADE-3 ANSWER SPANS (the answer key; the rater never saw these):\n")
	for _, j := range q.Judgements {
		if j.Grade != retrieval.GradeMax {
			continue
		}
		b.WriteString(fmt.Sprintf("- %s:%d-%d anchor=%q reason=%s\n", j.Path, j.StartLine, j.EndLine, j.Anchor, j.Reason))
	}
	b.WriteString("\n----- BEGIN THE EXACT BUNDLE THE RATER WAS GIVEN -----\n")
	b.Write(bundle.Payload.Bytes)
	b.WriteString("----- END THE EXACT BUNDLE THE RATER WAS GIVEN -----\n\n")
	b.WriteString("----- BEGIN THE RATER'S RESPONSE -----\n")
	b.WriteString(response.Text)
	b.WriteString("\n----- END THE RATER'S RESPONSE -----\n")
	return b.String()
}
