//go:build linux

package retrieval

import (
	"fmt"
	"os"
	"strings"
)

func probeQualificationReferenceMachine() (ReferenceMachine, error) {
	version, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return ReferenceMachine{}, fmt.Errorf("embedded-model qualification measure: read Linux OS version: %w", err)
	}
	cpuinfo, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return ReferenceMachine{}, fmt.Errorf("embedded-model qualification measure: read Linux CPU info: %w", err)
	}
	brand := ""
	cores := map[string]bool{}
	physical, core := "", ""
	flush := func() {
		if physical != "" && core != "" {
			cores[physical+":"+core] = true
		}
		physical, core = "", ""
	}
	for _, line := range strings.Split(string(cpuinfo), "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key, value := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		switch key {
		case "model name":
			if brand == "" {
				brand = value
			}
		case "physical id":
			physical = value
		case "core id":
			core = value
		}
	}
	flush()
	if brand == "" || len(cores) == 0 {
		return ReferenceMachine{}, fmt.Errorf("embedded-model qualification measure: Linux CPU identity or physical cores unavailable")
	}
	return ReferenceMachine{OS: "linux", OSVersion: strings.TrimSpace(string(version)), CPU: brand,
		PhysicalCores: len(cores)}, nil
}
