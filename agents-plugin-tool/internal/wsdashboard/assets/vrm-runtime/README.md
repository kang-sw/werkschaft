# Vendored Pure-VRM Companion Runtime

- Packages (pinned exact versions in `agents-plugin-tool/scripts/build-companion-vrm-runtime.mjs`):
  - `three` `0.187.0` (MIT, `LICENSE.three`)
  - `@pixiv/three-vrm` `3.6.0` (MIT, `LICENSE.three-vrm`)
  - `@pixiv/three-vrm-animation` `3.6.0` (MIT, `LICENSE.three-vrm-animation`)
  - Built with `esbuild` `0.28.2`
- Exported symbols: `THREE`, `GLTFLoader`, `VRMLoaderPlugin`, `VRMUtils`, `VRMAnimationLoaderPlugin`, `createVRMAnimationClip`.
- Output: `../companion-vrm-runtime.mjs`, served at content-hashed `/s/companion-vrm-<sha256[:12]>.mjs` behind the dashboard auth gate and loaded via dynamic `import()` only when the companion is enabled and the active pack uses `renderer: "vrm"`.
- Rebuild: `node agents-plugin-tool/scripts/build-companion-vrm-runtime.mjs`
