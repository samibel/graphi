//go:build darwin

package retrieval

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func probeQualificationReferenceMachine() (ReferenceMachine, error) {
	read := func(name string) (string, error) {
		out, err := exec.Command("sysctl", "-n", name).Output()
		if err != nil {
			return "", err
		}
		value := strings.TrimSpace(string(out))
		if value == "" {
			return "", fmt.Errorf("empty sysctl %s", name)
		}
		return value, nil
	}
	version, err := read("kern.osrelease")
	if err != nil {
		return ReferenceMachine{}, fmt.Errorf("embedded-model qualification measure: Darwin OS version: %w", err)
	}
	cpu, err := read("machdep.cpu.brand_string")
	if err != nil {
		cpu, err = read("hw.model")
	}
	if err != nil {
		return ReferenceMachine{}, fmt.Errorf("embedded-model qualification measure: Darwin CPU: %w", err)
	}
	physicalText, err := read("hw.physicalcpu")
	if err != nil {
		return ReferenceMachine{}, fmt.Errorf("embedded-model qualification measure: Darwin physical cores: %w", err)
	}
	physical, err := strconv.Atoi(physicalText)
	if err != nil || physical <= 0 {
		return ReferenceMachine{}, fmt.Errorf("embedded-model qualification measure: invalid Darwin physical cores")
	}
	return ReferenceMachine{OS: "darwin", OSVersion: version, CPU: cpu, PhysicalCores: physical}, nil
}
