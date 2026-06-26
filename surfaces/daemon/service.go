package daemon

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUnsupportedOS is returned by GenerateServiceUnit for a platform without a
// supported service manager (anything other than darwin/launchd or linux/systemd).
var ErrUnsupportedOS = errors.New("daemon: OS service install not supported on this platform")

// ServiceOptions parameterizes the generated OS service unit.
type ServiceOptions struct {
	Label      string   // service label / unit name stem (e.g. "com.graphi.daemon")
	BinaryPath string   // absolute path to the graphi binary
	Args       []string // daemon args, e.g. ["daemon", "start", "--socket", "/path.sock"]
}

// GenerateServiceUnit renders an OS service unit for the supported platforms:
// a launchd plist on darwin, a systemd user unit on linux. It returns the
// suggested filename, the unit content, or ErrUnsupportedOS for other platforms.
// It performs NO installation/activation itself (no system commands, no CGo) —
// the caller (an operator step) loads the unit. Output is deterministic for a
// given (goos, opts).
func GenerateServiceUnit(goos string, opts ServiceOptions) (filename, content string, err error) {
	if strings.TrimSpace(opts.Label) == "" || strings.TrimSpace(opts.BinaryPath) == "" {
		return "", "", fmt.Errorf("daemon: service unit requires Label and BinaryPath")
	}
	switch goos {
	case "darwin":
		return opts.Label + ".plist", launchdPlist(opts), nil
	case "linux":
		return opts.Label + ".service", systemdUnit(opts), nil
	default:
		return "", "", fmt.Errorf("%w: %s", ErrUnsupportedOS, goos)
	}
}

func launchdPlist(opts ServiceOptions) string {
	var args strings.Builder
	args.WriteString("\t\t<string>" + opts.BinaryPath + "</string>\n")
	for _, a := range opts.Args {
		args.WriteString("\t\t<string>" + a + "</string>\n")
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + opts.Label + `</string>
	<key>ProgramArguments</key>
	<array>
` + args.String() + `	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
</dict>
</plist>
`
}

func systemdUnit(opts ServiceOptions) string {
	exec := opts.BinaryPath
	if len(opts.Args) > 0 {
		exec += " " + strings.Join(opts.Args, " ")
	}
	return `[Unit]
Description=graphi local-first hot-index daemon (` + opts.Label + `)
After=default.target

[Service]
Type=simple
ExecStart=` + exec + `
Restart=on-failure

[Install]
WantedBy=default.target
`
}
