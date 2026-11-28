#!/usr/bin/env node
// Reproducible bundler for Werkschaft's lazy-loaded pure-VRM companion runtime.
// Produces agents-plugin-tool/internal/wsdashboard/assets/companion-vrm-runtime.mjs
// and copies upstream MIT license files into assets/vrm-runtime/.
//
// Usage:
//   node agents-plugin-tool/scripts/build-companion-vrm-runtime.mjs [--pkg-dir /path/to/pkg]
//
// When --pkg-dir (or WS_VRM_PKG_DIR) is omitted, installs pinned exact versions
// in a temporary directory and removes it on exit.

import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import zlib from "node:zlib";

const PINS = {
  three: "0.187.0",
  "@pixiv/three-vrm": "3.6.0",
  "@pixiv/three-vrm-animation": "3.6.0",
  esbuild: "0.28.2",
};

const MAX_GZIP_BYTES = 300 * 1024; // 300 KiB hard acceptance gate
const MAX_STUDIO_GZIP_BYTES = 360 * 1024; // 360 KiB hard acceptance gate for studio runtime

const ENTRY_SOURCE = [
  "export * as THREE from 'three';",
  "export { GLTFLoader } from 'three/examples/jsm/loaders/GLTFLoader.js';",
  "export { VRMLoaderPlugin, VRMUtils } from '@pixiv/three-vrm';",
  "export { VRMAnimationLoaderPlugin, createVRMAnimationClip } from '@pixiv/three-vrm-animation';",
  "",
].join("\n");

const STUDIO_ENTRY_SOURCE = [
  "export * as THREE from 'three';",
  "export { GLTFLoader } from 'three/examples/jsm/loaders/GLTFLoader.js';",
  "export { OrbitControls } from 'three/examples/jsm/controls/OrbitControls.js';",
  "export { TransformControls } from 'three/examples/jsm/controls/TransformControls.js';",
  "export { VRMLoaderPlugin, VRMUtils } from '@pixiv/three-vrm';",
  "export { VRMAnimationLoaderPlugin, createVRMAnimationClip } from '@pixiv/three-vrm-animation';",
  "",
].join("\n");

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const repoToolRoot = path.resolve(scriptDir, "..");
const assetsDir = path.join(repoToolRoot, "internal", "wsdashboard", "assets");
const outBundle = path.join(assetsDir, "companion-vrm-runtime.mjs");
const outStudioBundle = path.join(assetsDir, "companion-rig-studio-runtime.mjs");
const licenseDir = path.join(assetsDir, "vrm-runtime");

function parseArgs(argv) {
  let pkgDir = process.env.WS_VRM_PKG_DIR || "";
  for (let i = 0; i < argv.length; i++) {
    if (argv[i] === "--pkg-dir" && argv[i + 1]) {
      pkgDir = argv[++i];
    }
  }
  return { pkgDir };
}

function verifyPinnedVersions(pkgDir) {
  for (const [pkg, expected] of Object.entries(PINS)) {
    const manifestPath = path.join(pkgDir, "node_modules", ...pkg.split("/"), "package.json");
    if (!fs.existsSync(manifestPath)) {
      throw new Error(`Missing ${pkg} in ${pkgDir}`);
    }
    const actual = JSON.parse(fs.readFileSync(manifestPath, "utf8")).version;
    if (actual !== expected) {
      throw new Error(`Version mismatch for ${pkg}: got ${actual}, want ${expected}`);
    }
  }
}

function main() {
  const { pkgDir: suppliedPkgDir } = parseArgs(process.argv.slice(2));
  let pkgDir = suppliedPkgDir ? path.resolve(suppliedPkgDir) : "";
  let createdTemp = false;

  try {
    if (!pkgDir) {
      pkgDir = fs.mkdtempSync(path.join(os.tmpdir(), "ws-companion-vrm-vendor-"));
      createdTemp = true;
      const pkgJson = {
        name: "ws-companion-vrm-vendor",
        private: true,
        dependencies: PINS,
      };
      fs.writeFileSync(path.join(pkgDir, "package.json"), JSON.stringify(pkgJson, null, 2) + "\n");
      execFileSync("npm", ["install", "--no-audit", "--no-fund", "--omit=dev"], {
        cwd: pkgDir,
        stdio: "inherit",
      });
    }

    verifyPinnedVersions(pkgDir);

    const entryFile = path.join(pkgDir, "ws-vrm-entry.mjs");
    const studioEntryFile = path.join(pkgDir, "ws-vrm-studio-entry.mjs");
    fs.writeFileSync(entryFile, ENTRY_SOURCE);
    fs.writeFileSync(studioEntryFile, STUDIO_ENTRY_SOURCE);

    const esbuildBin = path.join(
      pkgDir,
      "node_modules",
      ".bin",
      process.platform === "win32" ? "esbuild.cmd" : "esbuild"
    );
    const buildBundle = (inFile, outFile, maxGzipBytes) => {
      execFileSync(
        esbuildBin,
        [
          inFile,
          "--bundle",
          "--format=esm",
          "--minify",
          "--target=es2022",
          "--legal-comments=none",
          `--outfile=${outFile}`,
        ],
        { cwd: pkgDir, stdio: "inherit" }
      );
      const raw = fs.readFileSync(outFile);
      const gz = zlib.gzipSync(raw, { level: 9 });
      console.log(
        `Built ${path.relative(repoToolRoot, outFile)}: ${raw.length} bytes raw, ${gz.length} bytes gzip (limit ${maxGzipBytes})`
      );
      if (gz.length > maxGzipBytes) {
        throw new Error(`Gzipped bundle ${path.basename(outFile)} (${gz.length} bytes) exceeds ${maxGzipBytes} bytes limit`);
      }
    };

    buildBundle(entryFile, outBundle, MAX_GZIP_BYTES);
    buildBundle(studioEntryFile, outStudioBundle, MAX_STUDIO_GZIP_BYTES);

    if (!createdTemp) {
      fs.rmSync(entryFile, { force: true });
      fs.rmSync(studioEntryFile, { force: true });
    }

    fs.mkdirSync(licenseDir, { recursive: true });
    const licenseCopies = [
      ["three", "LICENSE.three"],
      ["@pixiv/three-vrm", "LICENSE.three-vrm"],
      ["@pixiv/three-vrm-animation", "LICENSE.three-vrm-animation"],
    ];
    for (const [pkg, destName] of licenseCopies) {
      const src = path.join(pkgDir, "node_modules", ...pkg.split("/"), "LICENSE");
      fs.copyFileSync(src, path.join(licenseDir, destName));
    }
  } finally {
    if (createdTemp && pkgDir) {
      fs.rmSync(pkgDir, { recursive: true, force: true });
    }
  }
}

main();
