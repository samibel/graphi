// Command publish-lock is the CT-01 containment gate: it decides, from a
// checked-in reversible gate file (.github/publish-lock.json), whether a
// publish may proceed. The release DAG (release-dag.yml, SW-120/REL-01) runs
// it in its gate job and conditions the single mutating publish job on the
// emitted `locked` output, so NO trigger — merge or manual dispatch — can
// publish while the lock is engaged. RC-01 lifts the lock with one documented
// change — flipping "locked" to false in the gate file — with no workflow
// re-authoring.
//
// It fails closed (see Evaluate): a missing/broken gate file is treated as
// locked, so a deliberately-red gate yields no tag and no release.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	gate := flag.String("gate", ".github/publish-lock.json", "path to the reversible publish-lock gate file")
	flag.Parse()

	d := Evaluate(*gate)

	// Publish the decision to the workflow so downstream steps can gate on it.
	if out := os.Getenv("GITHUB_OUTPUT"); out != "" {
		f, err := os.OpenFile(out, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "publish-lock: cannot write GITHUB_OUTPUT: %v\n", err)
			os.Exit(2)
		}
		if _, err := fmt.Fprintln(f, d.OutputLine()); err != nil {
			f.Close()
			fmt.Fprintf(os.Stderr, "publish-lock: cannot write GITHUB_OUTPUT: %v\n", err)
			os.Exit(2)
		}
		f.Close()
	}

	if d.Locked {
		fmt.Println(d.Notice())
		fmt.Printf("publish-lock: LOCKED — %s\n", d.Reason)
		return
	}
	fmt.Printf("publish-lock: UNLOCKED — %s\n", d.Reason)
}
