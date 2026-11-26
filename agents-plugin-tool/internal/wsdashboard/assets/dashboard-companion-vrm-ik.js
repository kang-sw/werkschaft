(function (C) {
  "use strict";

  // ============================================================================
  // Procedural 2-Bone Analytical Arm IK & VRM Humanoid Finger Presets
  // ============================================================================
  // Proportion-independent shoulder -> elbow -> hand analytical IK solver with
  // elbow pole vectors and full VRM 0.x / 1.0 world-space parity, plus 5 VRM
  // standard humanoid finger presets that degrade silently when finger bones
  // are absent.

  const DEG = Math.PI / 180;

  // Five VRM humanoid finger pose presets: curl values in radians per finger
  // [proximal, intermediate, distal] (thumb uses [metacarpalY, proximalZ, distalZ]).
  const FINGER_PRESETS = Object.freeze({
    relaxed: Object.freeze({
      thumb: [0.16, 0.10, 0.08],
      index: [0.18, 0.22, 0.14],
      middle: [0.22, 0.26, 0.16],
      ring: [0.26, 0.30, 0.18],
      little: [0.30, 0.34, 0.20]
    }),
    open: Object.freeze({
      thumb: [0.06, 0.02, 0.02],
      index: [0.04, 0.04, 0.03],
      middle: [0.04, 0.05, 0.03],
      ring: [0.06, 0.06, 0.04],
      little: [0.08, 0.08, 0.05]
    }),
    hold_phone: Object.freeze({
      thumb: [0.34, 0.16, 0.18],
      index: [0.58, 0.68, 0.42],
      middle: [0.64, 0.74, 0.46],
      ring: [0.68, 0.76, 0.48],
      little: [0.70, 0.78, 0.50]
    }),
    point: Object.freeze({
      thumb: [0.42, 0.32, 0.24],
      index: [0.02, 0.03, 0.02],
      middle: [0.85, 0.92, 0.58],
      ring: [0.90, 0.96, 0.62],
      little: [0.92, 0.98, 0.64]
    }),
    cup: Object.freeze({
      thumb: [0.26, 0.18, 0.14],
      index: [0.36, 0.44, 0.30],
      middle: [0.38, 0.46, 0.32],
      ring: [0.40, 0.48, 0.34],
      little: [0.42, 0.50, 0.36]
    })
  });

  const FINGER_BONES = [
    ["thumb", "ThumbMetacarpal", "ThumbProximal", "ThumbDistal"],
    ["index", "IndexProximal", "IndexIntermediate", "IndexDistal"],
    ["middle", "MiddleProximal", "MiddleIntermediate", "MiddleDistal"],
    ["ring", "RingProximal", "RingIntermediate", "RingDistal"],
    ["little", "LittleProximal", "LittleIntermediate", "LittleDistal"]
  ];

  function conjugateEuler(x, y, z, isVrm0) {
    if (C.vrmMath?.conjugateVrm0Euler) return C.vrmMath.conjugateVrm0Euler(x, y, z, isVrm0);
    return isVrm0 ? [-x, y, -z] : [x, y, z];
  }

  function createIKScratch(THREE) {
    return {
      toTarget: new THREE.Vector3(),
      dir: new THREE.Vector3(),
      clampedTarget: new THREE.Vector3(),
      toPole: new THREE.Vector3(),
      bendDir: new THREE.Vector3(),
      ref: new THREE.Vector3(),
      elbowPos: new THREE.Vector3(),
      uDirW: new THREE.Vector3(),
      fDirW: new THREE.Vector3(),
      invParent: new THREE.Quaternion(),
      uDirP: new THREE.Vector3(),
      bendDirP: new THREE.Vector3(),
      curElbow: new THREE.Vector3(),
      e1: new THREE.Vector3(),
      e2: new THREE.Vector3(),
      cross: new THREE.Vector3(),
      qSwing: new THREE.Quaternion(),
      qTwist: new THREE.Quaternion(),
      upperQuat: new THREE.Quaternion(),
      upperWorldQuat: new THREE.Quaternion(),
      invUpperWorld: new THREE.Quaternion(),
      fDirUpper: new THREE.Vector3(),
      lowerQuat: new THREE.Quaternion(),
      out: {
        upperQuat: null,
        lowerQuat: null,
        elbowPos: null,
        handPos: null,
        clampedDistance: 0
      }
    };
  }

  // Pure analytical 2-bone IK solver. Computes exact local quaternions for
  // upperArm and lowerArm given world-space shoulder/target/pole positions,
  // bone segment lengths, parent world orientation, and local rest directions.
  // When params.scratch is supplied (as in the hot render loop), zero Three.js
  // vectors or quaternions are allocated.
  function solveTwoBoneArmIK(THREE, params) {
    const {
      shoulderPos,
      targetPos,
      polePos,
      upperLen,
      lowerLen,
      parentWorldQuat,
      restUpperDir,
      restLowerDir,
      restElbowDir,
      scratch
    } = params;

    const s = scratch || createIKScratch(THREE);
    const L1 = Math.max(0.02, upperLen);
    const L2 = Math.max(0.02, lowerLen);
    s.toTarget.subVectors(targetPos, shoulderPos);
    const rawDist = s.toTarget.length();
    if (rawDist > 1e-5) {
      s.dir.copy(s.toTarget).divideScalar(rawDist);
    } else {
      s.dir.set(0, -1, 0);
    }

    const minDist = Math.abs(L1 - L2) + 1e-4;
    const maxDist = (L1 + L2) * 0.995;
    const dist = Math.min(maxDist, Math.max(minDist, rawDist));
    s.clampedTarget.copy(shoulderPos).addScaledVector(s.dir, dist);

    const cosA = Math.min(1, Math.max(-1, (L1 * L1 + dist * dist - L2 * L2) / (2 * L1 * dist)));
    const sinA = Math.sqrt(Math.max(0, 1 - cosA * cosA));

    // Project pole direction perpendicular to shoulder->target line.
    s.toPole.subVectors(polePos, shoulderPos);
    s.bendDir.copy(s.toPole).addScaledVector(s.dir, -s.toPole.dot(s.dir));
    if (s.bendDir.lengthSq() < 1e-8) {
      if (Math.abs(s.dir.z) < 0.9) s.ref.set(0, 0, -1);
      else s.ref.set(0, 1, 0);
      s.bendDir.crossVectors(s.dir, s.ref);
    }
    s.bendDir.normalize();

    s.elbowPos.copy(shoulderPos)
      .addScaledVector(s.dir, L1 * cosA)
      .addScaledVector(s.bendDir, L1 * sinA);

    s.uDirW.subVectors(s.elbowPos, shoulderPos).normalize();
    s.fDirW.subVectors(s.clampedTarget, s.elbowPos).normalize();

    s.invParent.copy(parentWorldQuat).invert();
    s.uDirP.copy(s.uDirW).applyQuaternion(s.invParent).normalize();
    s.bendDirP.copy(s.bendDir).applyQuaternion(s.invParent).normalize();

    s.qSwing.setFromUnitVectors(restUpperDir, s.uDirP);
    s.upperQuat.copy(s.qSwing);

    // Align upper-arm roll so the elbow hinges cleanly along the pole plane.
    if (restElbowDir) {
      s.curElbow.copy(restElbowDir).applyQuaternion(s.qSwing);
      s.e1.copy(s.curElbow).addScaledVector(s.uDirP, -s.curElbow.dot(s.uDirP));
      s.e2.copy(s.bendDirP).addScaledVector(s.uDirP, -s.bendDirP.dot(s.uDirP));
      if (s.e1.lengthSq() > 1e-6 && s.e2.lengthSq() > 1e-6) {
        s.e1.normalize();
        s.e2.normalize();
        const cosT = Math.min(1, Math.max(-1, s.e1.dot(s.e2)));
        s.cross.crossVectors(s.e1, s.e2);
        const sinT = s.uDirP.dot(s.cross);
        const twistAngle = Math.atan2(sinT, cosT);
        s.qTwist.setFromAxisAngle(s.uDirP, twistAngle);
        s.upperQuat.copy(s.qTwist).multiply(s.qSwing);
      }
    }

    s.upperWorldQuat.copy(parentWorldQuat).multiply(s.upperQuat);
    s.invUpperWorld.copy(s.upperWorldQuat).invert();
    s.fDirUpper.copy(s.fDirW).applyQuaternion(s.invUpperWorld).normalize();
    s.lowerQuat.setFromUnitVectors(restLowerDir, s.fDirUpper);

    s.out.upperQuat = s.upperQuat;
    s.out.lowerQuat = s.lowerQuat;
    s.out.elbowPos = s.elbowPos;
    s.out.handPos = s.clampedTarget;
    s.out.clampedDistance = dist;
    return s.out;
  }

  function createArmIKSolver(THREE, vrm, isVrm0) {
    if (typeof THREE?.Vector3?.prototype?.subVectors !== "function" ||
        typeof THREE?.Quaternion?.prototype?.setFromUnitVectors !== "function") {
      return null;
    }
    const sides = Object.create(null);
    const fingerState = new Map();
    const armQuatCache = new Map();

    function inspectSide(side) {
      const upperName = side + "UpperArm";
      const lowerName = side + "LowerArm";
      const handName = side + "Hand";
      const upper = vrm.humanoid?.getNormalizedBoneNode(upperName);
      const lower = vrm.humanoid?.getNormalizedBoneNode(lowerName);
      const hand = vrm.humanoid?.getNormalizedBoneNode(handName);
      if (!upper || !lower || !hand) return null;

      // Measure segment lengths and local rest bone axes from T-pose offsets.
      const fallbackSign = (side === "left" ? 1 : -1) * (isVrm0 ? -1 : 1);
      const fallbackDir = new THREE.Vector3(fallbackSign, 0, 0);

      let upperLen = lower.position.length();
      let lowerLen = hand.position.length();
      const restUpperDir = upperLen > 1e-4 ? lower.position.clone().divideScalar(upperLen) : fallbackDir.clone();
      const restLowerDir = lowerLen > 1e-4 ? hand.position.clone().divideScalar(lowerLen) : fallbackDir.clone();

      if (upperLen <= 1e-4 || lowerLen <= 1e-4) {
        const sPos = new THREE.Vector3(), ePos = new THREE.Vector3(), hPos = new THREE.Vector3();
        upper.getWorldPosition(sPos);
        lower.getWorldPosition(ePos);
        hand.getWorldPosition(hPos);
        if (upperLen <= 1e-4) upperLen = Math.max(0.16, sPos.distanceTo(ePos));
        if (lowerLen <= 1e-4) lowerLen = Math.max(0.14, ePos.distanceTo(hPos));
      }

      // World -Z (behind the elbow) expressed in local bone space.
      const restElbowDir = new THREE.Vector3(0, 0, isVrm0 ? 1 : -1);
      return { upper, lower, hand, upperName, lowerName, handName, upperLen, lowerLen, restUpperDir, restLowerDir, restElbowDir };
    }

    sides.left = inspectSide("left");
    sides.right = inspectSide("right");

    const tmpShoulder = new THREE.Vector3();
    const tmpFallbackPole = new THREE.Vector3();
    const tmpParentQ = new THREE.Quaternion();
    const tmpEuler = new THREE.Euler(0, 0, 0, "XYZ");
    const tmpHandQ = new THREE.Quaternion();
    const fallbackVrm0Q = new THREE.Quaternion().setFromAxisAngle(new THREE.Vector3(0, 1, 0), Math.PI);
    const identityQ = new THREE.Quaternion();
    const ikScratch = createIKScratch(THREE);

    function slerpBone(key, node, targetQuat, alpha) {
      let cur = armQuatCache.get(key);
      if (!cur) {
        cur = node.quaternion.clone();
        armQuatCache.set(key, cur);
      }
      cur.slerp(targetQuat, alpha);
      node.quaternion.copy(cur);
    }

    function solveArm(side, worldTarget, worldPole, opts = {}) {
      const rigSide = sides[side];
      if (!rigSide) return null;
      const dt = opts.dt ?? 0.016;
      const speed = opts.speed ?? 12;
      const alpha = Math.min(1, Math.max(0.05, dt * speed));

      rigSide.upper.parent?.updateWorldMatrix?.(true, false);
      rigSide.upper.getWorldPosition(tmpShoulder);
      if (rigSide.upper.parent && typeof rigSide.upper.parent.getWorldQuaternion === "function") {
        rigSide.upper.parent.getWorldQuaternion(tmpParentQ);
      } else {
        tmpParentQ.copy(isVrm0 ? fallbackVrm0Q : identityQ);
      }

      const pole = worldPole || tmpFallbackPole.set(
        tmpShoulder.x + (side === "left" ? 0.18 : -0.18),
        tmpShoulder.y - 0.14,
        tmpShoulder.z - 0.22
      );

      const solved = solveTwoBoneArmIK(THREE, {
        shoulderPos: tmpShoulder,
        targetPos: worldTarget,
        polePos: pole,
        upperLen: rigSide.upperLen,
        lowerLen: rigSide.lowerLen,
        parentWorldQuat: tmpParentQ,
        restUpperDir: rigSide.restUpperDir,
        restLowerDir: rigSide.restLowerDir,
        restElbowDir: rigSide.restElbowDir,
        scratch: ikScratch
      });

      slerpBone(rigSide.upperName, rigSide.upper, solved.upperQuat, alpha);
      slerpBone(rigSide.lowerName, rigSide.lower, solved.lowerQuat, alpha);

      if (opts.handEuler) {
        const [hx, hy, hz] = conjugateEuler(opts.handEuler[0], opts.handEuler[1], opts.handEuler[2], isVrm0);
        tmpEuler.set(hx, hy, hz, "XYZ");
        tmpHandQ.setFromEuler(tmpEuler);
        slerpBone(rigSide.handName, rigSide.hand, tmpHandQ, alpha);
      }
      return solved;
    }

    function applyFingerPreset(side, presetName, dt = 0.016) {
      const preset = Object.hasOwn(FINGER_PRESETS, presetName) ? FINGER_PRESETS[presetName] : FINGER_PRESETS.relaxed;
      const curlSign = side === "left" ? -1 : 1;
      const alpha = Math.min(1, Math.max(0.08, dt * 14));
      let applied = 0;

      for (const [fingerKey, b0, b1, b2] of FINGER_BONES) {
        const vals = preset[fingerKey];
        const names = [side + b0, side + b1, side + b2];
        for (let i = 0; i < 3; i++) {
          const boneName = names[i];
          const node = vrm.humanoid?.getNormalizedBoneNode(boneName);
          if (!node) continue;
          applied++;
          let rx = 0, ry = 0, rz = 0;
          if (fingerKey === "thumb") {
            if (i === 0) {
              ry = -curlSign * vals[0];
              rz = curlSign * vals[0] * 0.35;
            } else {
              rz = curlSign * vals[i];
            }
          } else {
            rz = curlSign * vals[i];
          }
          const [tx, ty, tz] = conjugateEuler(rx, ry, rz, isVrm0);
          let cur = fingerState.get(boneName);
          if (!cur) {
            cur = [tx, ty, tz];
            fingerState.set(boneName, cur);
          } else {
            cur[0] += (tx - cur[0]) * alpha;
            cur[1] += (ty - cur[1]) * alpha;
            cur[2] += (tz - cur[2]) * alpha;
          }
          node.rotation.set(cur[0], cur[1], cur[2], "XYZ");
        }
      }
      return applied;
    }

    function syncArmFromCurrentBones(side) {
      const targets = side ? [sides[side]] : [sides.left, sides.right];
      for (const rigSide of targets) {
        if (!rigSide) continue;
        for (const [key, node] of [
          [rigSide.upperName, rigSide.upper],
          [rigSide.lowerName, rigSide.lower],
          [rigSide.handName, rigSide.hand]
        ]) {
          let cur = armQuatCache.get(key);
          if (!cur) {
            cur = node.quaternion.clone();
            armQuatCache.set(key, cur);
          } else {
            cur.copy(node.quaternion);
          }
        }
      }
    }

    function syncFingersFromCurrentBones() {
      for (const side of ["left", "right"]) {
        for (const [, b0, b1, b2] of FINGER_BONES) {
          for (const suffix of [b0, b1, b2]) {
            const boneName = side + suffix;
            const node = vrm.humanoid?.getNormalizedBoneNode(boneName);
            if (!node) continue;
            let cur = fingerState.get(boneName);
            if (!cur) {
              fingerState.set(boneName, [node.rotation.x, node.rotation.y, node.rotation.z]);
            } else {
              cur[0] = node.rotation.x;
              cur[1] = node.rotation.y;
              cur[2] = node.rotation.z;
            }
          }
        }
      }
    }

    function syncFromCurrentBones() {
      syncArmFromCurrentBones();
      syncFingersFromCurrentBones();
    }

    return {
      solveArm,
      applyFingerPreset,
      syncArmFromCurrentBones,
      syncFingersFromCurrentBones,
      syncFromCurrentBones,
      getSideMetrics(side) {
        const s = sides[side];
        return s ? { upperLen: s.upperLen, lowerLen: s.lowerLen } : null;
      }
    };
  }

  C.vrmIK = {
    DEG,
    FINGER_PRESETS,
    solveTwoBoneArmIK,
    createArmIKSolver
  };
})(window.WsCompanion);
