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

  // Pure analytical 2-bone IK solver. Computes exact local quaternions for
  // upperArm and lowerArm given world-space shoulder/target/pole positions,
  // bone segment lengths, parent world orientation, and local rest directions.
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
      restElbowDir
    } = params;

    const L1 = Math.max(0.02, upperLen);
    const L2 = Math.max(0.02, lowerLen);
    const toTarget = new THREE.Vector3().subVectors(targetPos, shoulderPos);
    const rawDist = toTarget.length();
    const dir = rawDist > 1e-5
      ? toTarget.clone().divideScalar(rawDist)
      : new THREE.Vector3(0, -1, 0);

    const minDist = Math.abs(L1 - L2) + 1e-4;
    const maxDist = (L1 + L2) * 0.995;
    const dist = Math.min(maxDist, Math.max(minDist, rawDist));
    const clampedTarget = shoulderPos.clone().addScaledVector(dir, dist);

    const cosA = Math.min(1, Math.max(-1, (L1 * L1 + dist * dist - L2 * L2) / (2 * L1 * dist)));
    const sinA = Math.sqrt(Math.max(0, 1 - cosA * cosA));

    // Project pole direction perpendicular to shoulder->target line.
    const toPole = new THREE.Vector3().subVectors(polePos, shoulderPos);
    const bendDir = toPole.addScaledVector(dir, -toPole.dot(dir));
    if (bendDir.lengthSq() < 1e-8) {
      const ref = Math.abs(dir.z) < 0.9 ? new THREE.Vector3(0, 0, -1) : new THREE.Vector3(0, 1, 0);
      bendDir.crossVectors(dir, ref);
    }
    bendDir.normalize();

    const elbowPos = shoulderPos.clone()
      .addScaledVector(dir, L1 * cosA)
      .addScaledVector(bendDir, L1 * sinA);

    const uDirW = new THREE.Vector3().subVectors(elbowPos, shoulderPos).normalize();
    const fDirW = new THREE.Vector3().subVectors(clampedTarget, elbowPos).normalize();

    const invParent = parentWorldQuat.clone().invert();
    const uDirP = uDirW.clone().applyQuaternion(invParent).normalize();
    const bendDirP = bendDir.clone().applyQuaternion(invParent).normalize();

    const qSwing = new THREE.Quaternion().setFromUnitVectors(restUpperDir, uDirP);
    let upperQuat = qSwing;

    // Align upper-arm roll so the elbow hinges cleanly along the pole plane.
    if (restElbowDir) {
      const curElbow = restElbowDir.clone().applyQuaternion(qSwing);
      const e1 = curElbow.addScaledVector(uDirP, -curElbow.dot(uDirP));
      const e2 = bendDirP.clone().addScaledVector(uDirP, -bendDirP.dot(uDirP));
      if (e1.lengthSq() > 1e-6 && e2.lengthSq() > 1e-6) {
        e1.normalize();
        e2.normalize();
        const cosT = Math.min(1, Math.max(-1, e1.dot(e2)));
        const cross = new THREE.Vector3().crossVectors(e1, e2);
        const sinT = uDirP.dot(cross);
        const twistAngle = Math.atan2(sinT, cosT);
        const qTwist = new THREE.Quaternion().setFromAxisAngle(uDirP, twistAngle);
        upperQuat = qTwist.multiply(qSwing);
      }
    }

    const upperWorldQuat = parentWorldQuat.clone().multiply(upperQuat);
    const fDirUpper = fDirW.clone().applyQuaternion(upperWorldQuat.clone().invert()).normalize();
    const lowerQuat = new THREE.Quaternion().setFromUnitVectors(restLowerDir, fDirUpper);

    return {
      upperQuat,
      lowerQuat,
      elbowPos,
      handPos: clampedTarget,
      clampedDistance: dist
    };
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
    const tmpParentQ = new THREE.Quaternion();
    const tmpEuler = new THREE.Euler(0, 0, 0, "XYZ");
    const tmpHandQ = new THREE.Quaternion();
    const fallbackVrm0Q = new THREE.Quaternion().setFromAxisAngle(new THREE.Vector3(0, 1, 0), Math.PI);
    const identityQ = new THREE.Quaternion();

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

      const pole = worldPole || new THREE.Vector3(
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
        restElbowDir: rigSide.restElbowDir
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

    function syncFromCurrentBones() {
      armQuatCache.clear();
      fingerState.clear();
    }

    return {
      solveArm,
      applyFingerPreset,
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
