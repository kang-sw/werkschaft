package wsdashboard

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"net/http"
	"sync"
)

// companionVrmRuntimeMJS is the lazy-loaded ES module bundling Three.js,
// @pixiv/three-vrm, and @pixiv/three-vrm-animation (assets/vrm-runtime/README.md).
// The outer shell imports it dynamically only when a VRM companion pack is active;
// it is never inlined into dashboard.tmpl and remains behind the dashboard auth gate.
//
//go:embed assets/companion-vrm-runtime.mjs
var companionVrmRuntimeMJS []byte

// companionRigStudioRuntimeMJS is the lazy-loaded ES module for the standalone
// Control-Rig Studio page (/companion/studio). It adds OrbitControls and
// TransformControls on top of the pure-VRM runtime and is never loaded by the
// main dashboard shell.
//
//go:embed assets/companion-rig-studio-runtime.mjs
var companionRigStudioRuntimeMJS []byte

// companionVrmLicense embeds the upstream MIT licenses beside the runtime bundle.
//
//go:embed assets/vrm-runtime/LICENSE.three assets/vrm-runtime/LICENSE.three-vrm assets/vrm-runtime/LICENSE.three-vrm-animation
var companionVrmLicense embed.FS

var companionVrmOnce = sync.OnceValues(func() (string, []byte) {
	sum := sha256.Sum256(companionVrmRuntimeMJS)
	return "companion-vrm-" + hex.EncodeToString(sum[:12]) + ".mjs", companionVrmRuntimeMJS
})

var companionRigStudioOnce = sync.OnceValues(func() (string, []byte) {
	sum := sha256.Sum256(companionRigStudioRuntimeMJS)
	return "companion-rig-studio-" + hex.EncodeToString(sum[:12]) + ".mjs", companionRigStudioRuntimeMJS
})

// CompanionVrmAsset returns the content-hashed filename and embedded bytes of
// the pure-VRM companion runtime ES module.
func CompanionVrmAsset() (name string, data []byte) { return companionVrmOnce() }

// CompanionRigStudioAsset returns the content-hashed filename and embedded bytes
// of the Control-Rig Studio ES module (Three.js + three-vrm + Orbit/TransformControls).
func CompanionRigStudioAsset() (name string, data []byte) { return companionRigStudioOnce() }

// serveCompanionVrm writes the immutable VRM runtime ES module. The route sits
// behind accessGuard (not publicAsset), matching the framed dashboard scripts.
func serveCompanionVrm(w http.ResponseWriter) {
	_, data := CompanionVrmAsset()
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}

// serveCompanionRigStudioRuntime writes the immutable Control-Rig Studio runtime
// ES module behind accessGuard.
func serveCompanionRigStudioRuntime(w http.ResponseWriter) {
	_, data := CompanionRigStudioAsset()
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}
