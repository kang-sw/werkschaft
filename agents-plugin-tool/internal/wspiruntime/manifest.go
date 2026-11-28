package wspiruntime

import (
	"fmt"
	"strings"
)

const (
	// PinnedVersion is the exact upstream earendil-works/pi release version
	// managed by ws.
	PinnedVersion = "1.5.2"
	// ReleaseTag is the GitHub release tag corresponding to PinnedVersion.
	ReleaseTag = "v" + PinnedVersion
	// DefaultBaseURL is the upstream GitHub release download prefix.
	DefaultBaseURL = "https://github.com/earendil-works/pi/releases/download/" + ReleaseTag + "/"
)

// ArchiveKind identifies how a standalone Pi release archive is packaged.
type ArchiveKind string

const (
	ArchiveTarGz ArchiveKind = "tar.gz"
	ArchiveZip   ArchiveKind = "zip"
)

// AssetSpec describes the pinned upstream standalone release asset for one
// (GOOS, GOARCH) platform.
type AssetSpec struct {
	GOOS        string
	GOARCH      string
	Filename    string
	SHA256      string
	ArchiveKind ArchiveKind
	BinaryName  string
}

// pinnedAssets holds the verbatim SHA256SUMS table for earendil-works/pi v1.5.2.
// Defense-in-depth verification compares both the fetched SHA256SUMS manifest
// and the streamed archive bytes against these pinned digests.
var pinnedAssets = map[string]AssetSpec{
	"darwin/arm64": {
		GOOS:        "darwin",
		GOARCH:      "arm64",
		Filename:    "pi-darwin-arm64.tar.gz",
		SHA256:      "56b5e194d485ced555667b8ba3276157a8272e9ecde9e001e428af80985bdd9b",
		ArchiveKind: ArchiveTarGz,
		BinaryName:  "pi",
	},
	"darwin/amd64": {
		GOOS:        "darwin",
		GOARCH:      "amd64",
		Filename:    "pi-darwin-x64.tar.gz",
		SHA256:      "f997a088613514ac97d913aeea7642094d84fe6d46854ff7e64e2c12e536dc50",
		ArchiveKind: ArchiveTarGz,
		BinaryName:  "pi",
	},
	"linux/amd64": {
		GOOS:        "linux",
		GOARCH:      "amd64",
		Filename:    "pi-linux-x64.tar.gz",
		SHA256:      "811c6f2c9e7aaee252b5404252e556fb713aa209c1f7f924226aa77d4ef70cb5",
		ArchiveKind: ArchiveTarGz,
		BinaryName:  "pi",
	},
	"linux/arm64": {
		GOOS:        "linux",
		GOARCH:      "arm64",
		Filename:    "pi-linux-arm64.tar.gz",
		SHA256:      "42805cd5615952fcbbfc9c56b04f8feb84395134952e169a6cdc8d7676975666",
		ArchiveKind: ArchiveTarGz,
		BinaryName:  "pi",
	},
	"windows/amd64": {
		GOOS:        "windows",
		GOARCH:      "amd64",
		Filename:    "pi-windows-x64.zip",
		SHA256:      "e6adb8cb8c84eca14391ad24b363de9d2be98a89ca3ba3fdee1fa4de32b7f2e8",
		ArchiveKind: ArchiveZip,
		BinaryName:  "pi.exe",
	},
	"windows/arm64": {
		GOOS:        "windows",
		GOARCH:      "arm64",
		Filename:    "pi-windows-arm64.zip",
		SHA256:      "e83404bc5a6003ac950683edd9b6cf499dac91ec890d8439a8c32528de534a77",
		ArchiveKind: ArchiveZip,
		BinaryName:  "pi.exe",
	},
}

// ResolveAsset returns the pinned AssetSpec for the given (goos, goarch) pair.
// It accepts both Go arch names ("amd64", "arm64") and upstream "x64".
func ResolveAsset(goos, goarch string) (AssetSpec, error) {
	goos = strings.ToLower(strings.TrimSpace(goos))
	goarch = strings.ToLower(strings.TrimSpace(goarch))
	if goarch == "x64" {
		goarch = "amd64"
	}
	spec, ok := pinnedAssets[goos+"/"+goarch]
	if !ok {
		return AssetSpec{}, fmt.Errorf("managed Pi runtime %s is not supported on %s/%s", ReleaseTag, goos, goarch)
	}
	return spec, nil
}
