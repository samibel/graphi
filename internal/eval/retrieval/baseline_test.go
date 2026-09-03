package retrieval

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func TestGrepRead_HandComputedGolden(t *testing.T) {
	got := GrepRead(os.DirFS("testdata/grepread-repo"), "alpha beta alpha!")

	if GrepReadVersion != "1" || GrepReadSearchLimit != 20 || GrepReadWindowLines != 40 || GrepReadMaxReads != 8 {
		t.Fatalf("parameter identity = %q/%d/%d/%d", GrepReadVersion, GrepReadSearchLimit, GrepReadWindowLines, GrepReadMaxReads)
	}
	if want := []string{"alpha", "beta"}; !reflect.DeepEqual(got.Patterns, want) {
		t.Fatalf("patterns = %q, want %q", got.Patterns, want)
	}
	if want := []string{"a.go", "nested/b.go", "nested/b_test.go"}; !reflect.DeepEqual(got.IncludedFiles, want) {
		t.Fatalf("included files = %q, want %q", got.IncludedFiles, want)
	}
	if got.StopReason != SavingsStopExhausted {
		t.Fatalf("stop reason = %q, want %q", got.StopReason, SavingsStopExhausted)
	}

	wantResponses := []struct {
		operation string
		bytes     []byte
		byteCount int
		sha256    string
	}{
		{
			operation: PayloadOperationGrep,
			bytes: []byte("a.go:3:6:func Alpha() {}\n" +
				"a.go:5:5:var Beta = Alpha\n" +
				"nested/b.go:3:4:// beta arrives here\n" +
				"nested/b_test.go:3:10:func TestAlpha(t *testing.T) {}\n"),
			byteCount: 142,
			sha256:    "56d9807b7f8f4d895f702659497e9f1d38c189348f892913b1f6b5df16a94181",
		},
		{
			operation: PayloadOperationRead,
			bytes:     []byte("func Alpha() {}\n\nvar Beta = Alpha\n"),
			byteCount: 34,
			sha256:    "a4eb7b29825fd8b4e7cd605b01d285c6198ca7eb55b458f7920eecc11bc22456",
		},
		{
			operation: PayloadOperationRead,
			bytes:     []byte("// beta arrives here\nfunc Gamma() {}\n"),
			byteCount: 37,
			sha256:    "00199c70d2b467d30dc73cc4b32693c88805549ec53106709e700de6b25c31e8",
		},
		{
			operation: PayloadOperationRead,
			bytes:     []byte("func TestAlpha(t *testing.T) {}\n"),
			byteCount: 32,
			sha256:    "2bfddf4edcc203cd30ee63b5b26fa48e4a0468cceae0438315b5517edf24f68c",
		},
	}
	if len(got.Ledger.Responses) != len(wantResponses) {
		t.Fatalf("response count = %d, want %d", len(got.Ledger.Responses), len(wantResponses))
	}
	for i, want := range wantResponses {
		response := got.Ledger.Responses[i]
		if response.Sequence != i+1 || response.Boundary != PayloadBoundaryGrepRead || response.Operation != want.operation {
			t.Fatalf("response %d identity = %+v", i+1, response)
		}
		if !reflect.DeepEqual(response.Bytes, want.bytes) {
			t.Fatalf("response %d bytes:\n%q\nwant:\n%q", i+1, response.Bytes, want.bytes)
		}
		if response.ByteCount != want.byteCount {
			t.Fatalf("response %d byte count = %d, want %d", i+1, response.ByteCount, want.byteCount)
		}
		if response.SHA256 != want.sha256 {
			t.Fatalf("response %d sha256 = %q, want %q", i+1, response.SHA256, want.sha256)
		}
	}

	// Hand derivation, independent of the implementation:
	//   grep = 25 + 26 + 37 + 54 = 142 bytes;
	//   reads = (16 + 1 + 17) + (21 + 16) + 32 = 103 bytes;
	//   complete cost = 142 + 103 = 245 bytes.
	// a.go:5 is not read again because the read starting at line 3 covers
	// lines 3..42. Text/hidden/vendor files are outside the inclusion rule.
	if got.Ledger.TotalByteCount() != 245 || len(got.Ledger.ConcatenatedBytes()) != 245 {
		t.Fatalf("complete payload cost = %d/%d bytes, want 245", got.Ledger.TotalByteCount(), len(got.Ledger.ConcatenatedBytes()))
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("golden transcript invalid: %v", err)
	}
}

func TestGrepRead_IsStructurallyJudgementBlindAndDeterministic(t *testing.T) {
	signature := reflect.TypeOf(GrepRead)
	if signature.NumIn() != 2 || signature.In(0) != reflect.TypeOf((*fs.FS)(nil)).Elem() || signature.In(1).Kind() != reflect.String || signature.NumOut() != 1 || signature.Out(0) != reflect.TypeOf(GrepReadTranscript{}) {
		t.Fatalf("GrepRead signature = %s, want func(fs.FS, string) GrepReadTranscript", signature)
	}

	clean := Query{
		Text: "alpha beta alpha!",
		Judgements: []Judgement{
			{Path: "a.go", StartLine: 3, EndLine: 3, Anchor: "Alpha", Grade: 3},
			{Path: "nested/b.go", StartLine: 3, EndLine: 3, Anchor: "beta", Grade: 2},
		},
	}
	poisoned := clean
	poisoned.Judgements = []Judgement{
		{Path: "does/not/exist.go", StartLine: 9000, EndLine: 9999, Anchor: "Gamma", Grade: 3},
		{Path: "a.go", StartLine: 5, EndLine: 5, Anchor: "Beta", Grade: 0},
	}

	run := func(query Query) GrepReadTranscript {
		// The only fields accepted by the public comparator seam are the
		// repository and query.Text. There is nowhere to pass Judgements.
		return GrepRead(os.DirFS("testdata/grepread-repo"), query.Text)
	}
	first := run(clean)
	second := run(clean)
	changedJudgements := run(poisoned)
	if first.DigestSHA256() != second.DigestSHA256() || first.Ledger.DigestSHA256() != second.Ledger.DigestSHA256() {
		t.Fatalf("identical inputs were nondeterministic:\nfirst transcript=%s payload=%s\nsecond transcript=%s payload=%s",
			first.DigestSHA256(), first.Ledger.DigestSHA256(), second.DigestSHA256(), second.Ledger.DigestSHA256())
	}
	if first.DigestSHA256() != changedJudgements.DigestSHA256() || first.Ledger.DigestSHA256() != changedJudgements.Ledger.DigestSHA256() {
		t.Fatalf("judgement mutation moved a comparator digest:\nclean transcript=%s payload=%s\npoisoned transcript=%s payload=%s",
			first.DigestSHA256(), first.Ledger.DigestSHA256(), changedJudgements.DigestSHA256(), changedJudgements.Ledger.DigestSHA256())
	}
	if !reflect.DeepEqual(first.Ledger.Responses, changedJudgements.Ledger.Responses) {
		t.Fatal("judgement mutation changed serialized response bytes")
	}
}

func TestPayloadLedger_CostIsEveryCapturedResponse(t *testing.T) {
	transcript := GrepRead(os.DirFS("testdata/grepread-repo"), "alpha beta alpha!")
	wantComplete := []byte("a.go:3:6:func Alpha() {}\n" +
		"a.go:5:5:var Beta = Alpha\n" +
		"nested/b.go:3:4:// beta arrives here\n" +
		"nested/b_test.go:3:10:func TestAlpha(t *testing.T) {}\n" +
		"func Alpha() {}\n\nvar Beta = Alpha\n" +
		"// beta arrives here\nfunc Gamma() {}\n" +
		"func TestAlpha(t *testing.T) {}\n")
	if got := transcript.Ledger.ConcatenatedBytes(); !bytes.Equal(got, wantComplete) {
		t.Fatalf("complete cost payload:\n%q\nwant:\n%q", got, wantComplete)
	}
	if transcript.Ledger.TotalByteCount() != len(wantComplete) {
		t.Fatalf("total byte count = %d, want len(complete payload)=%d", transcript.Ledger.TotalByteCount(), len(wantComplete))
	}
	if transcript.Ledger.DigestSHA256() != "0ffb7e504e2c91ef039ca77d1bab12b1cac55c225a3f30fb61acfd66284eba2c" {
		t.Fatalf("complete payload digest = %s", transcript.Ledger.DigestSHA256())
	}

	real := PayloadCounter{
		TokenizerID:      "fixture-byte-tokenizer",
		VocabularySHA256: testVocabularySHA,
		Count:            func(raw []byte) (int, error) { return len(raw), nil },
	}
	payloads, err := transcript.Ledger.PreservedPayloads(real)
	if err != nil {
		t.Fatalf("materialize captured payloads: %v", err)
	}
	wantBytes := []int{142, 34, 37, 32}
	wantWhitespace := []int{15, 7, 7, 4}
	for i, payload := range payloads {
		if payload.ByteCount != wantBytes[i] || payload.TokenCounts[0].Tokens != wantWhitespace[i] || payload.TokenCounts[1].Tokens != wantBytes[i] {
			t.Fatalf("payload %d counts = bytes:%d whitespace:%d real:%d, want %d/%d/%d", i+1,
				payload.ByteCount, payload.TokenCounts[0].Tokens, payload.TokenCounts[1].Tokens,
				wantBytes[i], wantWhitespace[i], wantBytes[i])
		}
		if !bytes.Equal(payload.Bytes, transcript.Ledger.Responses[i].Bytes) || payload.SHA256 != SHA256Hex(payload.Bytes) {
			t.Fatalf("payload %d was not derived from its captured byte slice", i+1)
		}
	}

	t.Run("token counter cannot mutate captured bytes", func(t *testing.T) {
		beforeDigest := transcript.Ledger.DigestSHA256()
		_, err := transcript.Ledger.PreservedPayloads(PayloadCounter{
			TokenizerID:      "fixture-mutating-tokenizer",
			VocabularySHA256: testVocabularySHA,
			Count: func(raw []byte) (int, error) {
				raw[0] = 'X'
				return len(raw), nil
			},
		})
		if err != nil {
			t.Fatalf("materialize with mutating counter: %v", err)
		}
		if transcript.Ledger.DigestSHA256() != beforeDigest {
			t.Fatalf("token counter mutated captured bytes: digest %s, want %s", transcript.Ledger.DigestSHA256(), beforeDigest)
		}
	})

	t.Run("dropping an intermediate read leaves a sequence hole", func(t *testing.T) {
		dropped := transcript
		dropped.Ledger.Responses = append([]CapturedPayload(nil), transcript.Ledger.Responses[:2]...)
		dropped.Ledger.Responses = append(dropped.Ledger.Responses, transcript.Ledger.Responses[3:]...)
		err := dropped.Validate()
		if err == nil || err.Error() != "retrieval payload ledger: response sequence=4, want 3" {
			t.Fatalf("error = %v, want sequence-hole rejection", err)
		}
		if dropped.Ledger.TotalByteCount() != 208 || dropped.Ledger.DigestSHA256() == transcript.Ledger.DigestSHA256() {
			t.Fatalf("dropped ledger cost/digest = %d/%s, complete = %d/%s",
				dropped.Ledger.TotalByteCount(), dropped.Ledger.DigestSHA256(), transcript.Ledger.TotalByteCount(), transcript.Ledger.DigestSHA256())
		}
	})

	t.Run("dropping the final read disagrees with the operation transcript", func(t *testing.T) {
		dropped := transcript
		dropped.Ledger.Responses = append([]CapturedPayload(nil), transcript.Ledger.Responses[:3]...)
		err := dropped.Validate()
		if err == nil || err.Error() != "retrieval GrepRead: ledger has 3 responses for grep plus 3 reads" {
			t.Fatalf("error = %v, want transcript-cost equality rejection", err)
		}
	})
}

func TestGrepRead_LedgerPreservesErrorAndEmptyResponses(t *testing.T) {
	t.Run("empty grep response", func(t *testing.T) {
		transcript := GrepRead(os.DirFS("testdata/grepread-repo"), "unfindable")
		if len(transcript.Ledger.Responses) != 1 || transcript.Ledger.Responses[0].Bytes == nil || len(transcript.Ledger.Responses[0].Bytes) != 0 {
			t.Fatalf("empty grep ledger = %+v", transcript.Ledger)
		}
		if transcript.StopReason != SavingsStopExhausted || transcript.Ledger.Responses[0].SHA256 != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
			t.Fatalf("empty grep stop/digest = %q/%s", transcript.StopReason, transcript.Ledger.Responses[0].SHA256)
		}
		if err := transcript.Validate(); err != nil {
			t.Fatalf("empty grep transcript invalid: %v", err)
		}
	})

	t.Run("invalid query grep error", func(t *testing.T) {
		transcript := GrepRead(os.DirFS("testdata/grepread-repo"), "?!")
		want := []byte("grep:error:query:no_searchable_pattern\n")
		if len(transcript.Ledger.Responses) != 1 || !bytes.Equal(transcript.Ledger.Responses[0].Bytes, want) {
			t.Fatalf("invalid-query grep ledger = %+v", transcript.Ledger)
		}
	})

	t.Run("search read error", func(t *testing.T) {
		repository := &mutatingReadFS{
			FS:      os.DirFS("testdata/grepread-repo"),
			reads:   map[string]int{},
			error:   "nested/b.go",
			errorAt: 1,
		}
		transcript := GrepRead(repository, "alpha beta alpha!")
		wantPrefix := "grep:error:nested/b.go:read_failed\n" +
			"a.go:3:6:func Alpha() {}\n" +
			"a.go:5:5:var Beta = Alpha\n"
		if !strings.HasPrefix(string(transcript.Ledger.Responses[0].Bytes), wantPrefix) {
			t.Fatalf("search error response = %q, want prefix %q", transcript.Ledger.Responses[0].Bytes, wantPrefix)
		}
		for _, read := range transcript.Reads {
			if read.Path == "nested/b.go" {
				t.Fatal("search-error file produced a read operation")
			}
		}
		if err := transcript.Validate(); err != nil {
			t.Fatalf("search-error transcript invalid: %v", err)
		}
	})

	t.Run("read error and empty read responses", func(t *testing.T) {
		repository := &mutatingReadFS{
			FS:      os.DirFS("testdata/grepread-repo"),
			reads:   map[string]int{},
			error:   "nested/b.go",
			errorAt: 2,
			empty:   "nested/b_test.go",
			emptyAt: 2,
		}
		transcript := GrepRead(repository, "alpha beta alpha!")
		if err := transcript.Validate(); err != nil {
			t.Fatalf("transcript with error/empty responses invalid: %v", err)
		}
		if len(transcript.Ledger.Responses) != 4 {
			t.Fatalf("response count = %d, want grep plus three reads", len(transcript.Ledger.Responses))
		}

		errorResponse := transcript.Ledger.Responses[2]
		wantError := []byte("read:error:nested/b.go:read_failed\n")
		if !bytes.Equal(errorResponse.Bytes, wantError) || errorResponse.ByteCount != 35 || errorResponse.SHA256 != "ee85f062a296fffd1f61be042909a7046c34e467d7dcd7a8c1cd4bf674ca984b" {
			t.Fatalf("error response = bytes:%q count:%d sha:%s", errorResponse.Bytes, errorResponse.ByteCount, errorResponse.SHA256)
		}
		emptyResponse := transcript.Ledger.Responses[3]
		if emptyResponse.Bytes == nil || len(emptyResponse.Bytes) != 0 || emptyResponse.ByteCount != 0 || emptyResponse.SHA256 != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
			t.Fatalf("empty response = bytes:%v count:%d sha:%s", emptyResponse.Bytes, emptyResponse.ByteCount, emptyResponse.SHA256)
		}

		counted := 0
		payloads, err := transcript.Ledger.PreservedPayloads(PayloadCounter{
			TokenizerID:      "fixture-count-calls",
			VocabularySHA256: testVocabularySHA,
			Count: func(raw []byte) (int, error) {
				counted++
				return len(raw), nil
			},
		})
		if err != nil {
			t.Fatalf("materialize error/empty responses: %v", err)
		}
		if counted != 4 || len(payloads) != 4 || payloads[3].Bytes == nil || payloads[3].TokenCounts[0].Tokens != 0 || payloads[3].TokenCounts[1].Tokens != 0 {
			t.Fatalf("materialized empty payload = calls:%d payload:%+v", counted, payloads[3])
		}
	})
}

func TestGrepRead_SearchAndReadCapsAreDeterministic(t *testing.T) {
	repository := fstest.MapFS{}
	for i := 0; i < GrepReadSearchLimit+1; i++ {
		repository[fmt.Sprintf("f%02d.go", i)] = &fstest.MapFile{Data: []byte("package p\n\nvar Alpha = 1\n")}
	}

	transcript := GrepRead(repository, "alpha")
	if len(transcript.IncludedFiles) != 21 {
		t.Fatalf("included files = %d, want all 21 eligible files", len(transcript.IncludedFiles))
	}
	grepLines := strings.Split(strings.TrimSuffix(string(transcript.Ledger.Responses[0].Bytes), "\n"), "\n")
	if len(grepLines) != GrepReadSearchLimit || grepLines[0] != "f00.go:3:5:var Alpha = 1" || grepLines[19] != "f19.go:3:5:var Alpha = 1" {
		t.Fatalf("limited grep response = %q", grepLines)
	}
	if strings.Contains(string(transcript.Ledger.Responses[0].Bytes), "f20.go") {
		t.Fatal("grep response exceeded SearchLimit")
	}
	if len(transcript.Reads) != GrepReadMaxReads || len(transcript.Ledger.Responses) != GrepReadMaxReads+1 || transcript.StopReason != SavingsStopMaxReads {
		t.Fatalf("read cap = reads:%d responses:%d stop:%q", len(transcript.Reads), len(transcript.Ledger.Responses), transcript.StopReason)
	}
	for i, read := range transcript.Reads {
		wantPath := fmt.Sprintf("f%02d.go", i)
		if read.Path != wantPath || read.StartLine != 3 || read.EndLine != 3 || read.ResponseSequence != i+2 {
			t.Fatalf("read %d = %+v, want path %s line 3", i+1, read, wantPath)
		}
	}
	if err := transcript.Validate(); err != nil {
		t.Fatalf("capped transcript invalid: %v", err)
	}
}

type mutatingReadFS struct {
	fs.FS
	reads   map[string]int
	error   string
	errorAt int
	empty   string
	emptyAt int
}

func (m *mutatingReadFS) ReadFile(name string) ([]byte, error) {
	m.reads[name]++
	if name == m.error && m.reads[name] == m.errorAt {
		return nil, errors.New("injected read failure")
	}
	if name == m.empty && m.reads[name] == m.emptyAt {
		return []byte{}, nil
	}
	return fs.ReadFile(m.FS, name)
}
