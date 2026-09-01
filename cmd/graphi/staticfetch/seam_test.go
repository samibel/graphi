// Package staticfetch's test seams. These wrappers exist ONLY so the
// production file (fetch.go) can stay free of test-only exports.
// Production callers never reach this file.
package staticfetch

import (
	"context"
	"net/http"
	"net/url"

	"github.com/samibel/graphi/engine/embed/static"
)

// DownloadForTest is the test-only entry point: like Download but with
// a controllable client and baseURL. The pin table is the package
// var static.PinnedSHA256 which the test mutates before calling and
// restores after — see swapPins.
func DownloadForTest(client *http.Client, baseURL, dest string) error {
	return downloadImpl(context.Background(), client, baseURL, dest, static.PinnedSHA256)
}

// InstallLocalForTest is the test-only entry point for the air-gapped
// path. It validates src against the (currently-set) PinnedSHA256
// table and copies the files to dest.
func InstallLocalForTest(src, dest string) error {
	return InstallLocal(context.Background(), src, dest)
}

// CheckRedirectForTest exercises the exact redirect policy installed on the
// production download client without opening a localhost listener.
func CheckRedirectForTest(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	req := &http.Request{URL: u}
	return newHTTPSOnlyClient().CheckRedirect(req, nil)
}
