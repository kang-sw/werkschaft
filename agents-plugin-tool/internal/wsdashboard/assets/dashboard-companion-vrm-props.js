(function (C) {
  "use strict";

  // ============================================================================
  // Phase 2 Procedural 3D Toon Props, IK Gesture Choreography & Frustum Bounds
  // ============================================================================
  // Renders lightweight procedural Three.js toon props (smartphone, subagent
  // drone, coffee mug, mail envelope) and proportion-independent 2-bone IK
  // gestures/poses kept strictly inside the 240x280 bust dock viewport.

  const VRM_POSES = Object.freeze({
    stand: {},
    typing: {},
    perched: {},
    dangle: {}
  });

  const VRM_GESTURES = Object.freeze({
    wave: {},
    point: {},
    headrub: {},
    cup_hands: {},
    coffee: {},
    perched: {},
    dangle: {}
  });

  const VRM_PROPS = Object.freeze({
    smartphone: {},
    drone: {},
    coffee: {},
    mail: {}
  });

  function createToonGradientMap(THREE) {
    if (typeof THREE.DataTexture !== "function") return null;
    const colors = new Uint8Array([
      92, 96, 112, 255,
      178, 184, 198, 255,
      255, 255, 255, 255
    ]);
    const tex = new THREE.DataTexture(colors, 3, 1);
    if (THREE.NearestFilter !== undefined) {
      tex.minFilter = THREE.NearestFilter;
      tex.magFilter = THREE.NearestFilter;
    }
    tex.generateMipmaps = false;
    tex.needsUpdate = true;
    return tex;
  }

  function makeToonMat(THREE, color, gradientMap, extras = {}) {
    if (typeof THREE.MeshToonMaterial === "function") {
      return new THREE.MeshToonMaterial({ color, gradientMap: gradientMap || null, ...extras });
    }
    if (typeof THREE.MeshBasicMaterial === "function") {
      return new THREE.MeshBasicMaterial({ color, ...extras });
    }
    return { color, dispose() {} };
  }

  function makeOutlineMat(THREE) {
    if (typeof THREE.MeshBasicMaterial === "function") {
      return new THREE.MeshBasicMaterial({ color: 0x151720, side: THREE.BackSide });
    }
    return { color: 0x151720, dispose() {} };
  }

  function addOutlinedMesh(THREE, parent, geometry, material, outlineMat, outlineScale = 1.07) {
    if (typeof THREE.Mesh !== "function") return null;
    const mesh = new THREE.Mesh(geometry, material);
    parent.add(mesh);
    if (outlineMat) {
      const outline = new THREE.Mesh(geometry, outlineMat);
      outline.scale.setScalar(outlineScale);
      mesh.add(outline);
    }
    return mesh;
  }

  function cubicBezier3(THREE, p0, p1, p2, p3, t, out) {
    const u = 1 - t;
    const b0 = u * u * u;
    const b1 = 3 * u * u * t;
    const b2 = 3 * u * t * t;
    const b3 = t * t * t;
    return out.set(
      p0.x * b0 + p1.x * b1 + p2.x * b2 + p3.x * b3,
      p0.y * b0 + p1.y * b1 + p2.y * b2 + p3.y * b3,
      p0.z * b0 + p1.z * b1 + p2.z * b2 + p3.z * b3
    );
  }

  function projectPointPx(THREE, worldPos, camera, width, height, tmp) {
    tmp.copy(worldPos).project(camera);
    return {
      x: (tmp.x * 0.5 + 0.5) * width,
      y: (-tmp.y * 0.5 + 0.5) * height,
      z: tmp.z
    };
  }

  // Clamp a world point into the camera's visible frustum inset by insetPx,
  // preserving its depth along the camera's forward axis.
  function clampWorldToInset(THREE, worldPos, camera, width, height, insetPx) {
    if (!camera || typeof worldPos.project !== "function" || typeof worldPos.unproject !== "function") {
      return worldPos;
    }
    const maxNdcX = Math.max(0.15, 1 - (2 * insetPx) / Math.max(64, width));
    const maxNdcY = Math.max(0.15, 1 - (2 * insetPx) / Math.max(64, height));
    const ndc = worldPos.clone().project(camera);
    const clampedX = Math.max(-maxNdcX, Math.min(maxNdcX, ndc.x));
    const clampedY = Math.max(-maxNdcY, Math.min(maxNdcY, ndc.y));
    if (clampedX === ndc.x && clampedY === ndc.y) return worldPos;
    ndc.x = clampedX;
    ndc.y = clampedY;
    return worldPos.copy(ndc.unproject(camera));
  }

  function buildSmartphone(THREE, grad, outlineMat, disposables) {
    const group = new THREE.Group();
    group.name = "ws-prop-smartphone";
    group.visible = false;

    const bodyGeo = new THREE.BoxGeometry(0.068, 0.122, 0.011);
    const screenGeo = new THREE.PlaneGeometry(0.056, 0.102);
    const barGeo = new THREE.PlaneGeometry(0.036, 0.008);
    const bodyMat = makeToonMat(THREE, 0x262b38, grad);
    const screenMat = typeof THREE.MeshBasicMaterial === "function"
      ? new THREE.MeshBasicMaterial({ color: 0x6fd0ff }) : makeToonMat(THREE, 0x6fd0ff, grad);
    const barMat = typeof THREE.MeshBasicMaterial === "function"
      ? new THREE.MeshBasicMaterial({ color: 0xd9f4ff }) : makeToonMat(THREE, 0xd9f4ff, grad);
    disposables.push(bodyGeo, screenGeo, barGeo, bodyMat, screenMat, barMat);

    addOutlinedMesh(THREE, group, bodyGeo, bodyMat, outlineMat, 1.08);
    const screen = new THREE.Mesh(screenGeo, screenMat);
    screen.position.set(0, 0, -0.0062);
    screen.rotation.y = Math.PI; // screen faces avatar (-Z)
    group.add(screen);

    for (let i = 0; i < 3; i++) {
      const bar = new THREE.Mesh(barGeo, barMat);
      bar.position.set(0, 0.026 - i * 0.02, -0.0068);
      bar.rotation.y = Math.PI;
      group.add(bar);
    }

    // Subtle screen bounce glow on the avatar's face while typing.
    let light = null;
    if (typeof THREE.PointLight === "function") {
      light = new THREE.PointLight(0x8ad4ff, 0, 0.95);
      light.position.set(0, 0.02, -0.05);
      group.add(light);
    }
    return { group, light, radius: 0.068 };
  }

  function buildDrone(THREE, grad, outlineMat, disposables) {
    const group = new THREE.Group();
    group.name = "ws-prop-drone";
    group.visible = false;

    const coreGeo = new THREE.CylinderGeometry(0.030, 0.026, 0.018, 8);
    const ringGeo = new THREE.TorusGeometry(0.032, 0.004, 6, 16);
    const eyeGeo = new THREE.SphereGeometry(0.008, 8, 8);
    const armGeo = new THREE.BoxGeometry(0.088, 0.005, 0.006);
    const rotorGeo = new THREE.CylinderGeometry(0.020, 0.020, 0.0025, 10);

    const coreMat = makeToonMat(THREE, 0x303748, grad);
    const ringMat = makeToonMat(THREE, 0x3ec8b8, grad);
    const eyeMat = typeof THREE.MeshBasicMaterial === "function"
      ? new THREE.MeshBasicMaterial({ color: 0x8ef3ff }) : makeToonMat(THREE, 0x8ef3ff, grad);
    const rotorMat = typeof THREE.MeshBasicMaterial === "function"
      ? new THREE.MeshBasicMaterial({ color: 0x9be8ff, transparent: true, opacity: 0.72 })
      : makeToonMat(THREE, 0x9be8ff, grad);
    disposables.push(coreGeo, ringGeo, eyeGeo, armGeo, rotorGeo, coreMat, ringMat, eyeMat, rotorMat);

    addOutlinedMesh(THREE, group, coreGeo, coreMat, outlineMat, 1.09);
    const ring = new THREE.Mesh(ringGeo, ringMat);
    ring.rotation.x = Math.PI * 0.5;
    group.add(ring);

    const eye = new THREE.Mesh(eyeGeo, eyeMat);
    eye.position.set(0, 0, 0.028);
    group.add(eye);

    for (const angle of [Math.PI * 0.25, -Math.PI * 0.25]) {
      const arm = new THREE.Mesh(armGeo, coreMat);
      arm.rotation.y = angle;
      group.add(arm);
    }

    const rotors = [];
    const offsets = [[0.032, 0.014, 0.032], [-0.032, 0.014, 0.032], [0.032, 0.014, -0.032], [-0.032, 0.014, -0.032]];
    for (const [rx, ry, rz] of offsets) {
      const rotor = new THREE.Mesh(rotorGeo, rotorMat);
      rotor.position.set(rx, ry, rz);
      group.add(rotor);
      rotors.push(rotor);
    }
    return { group, rotors, radius: 0.056 };
  }

  function buildCoffeeMug(THREE, grad, outlineMat, disposables) {
    const group = new THREE.Group();
    group.name = "ws-prop-coffee";
    group.visible = false;

    const cupGeo = new THREE.CylinderGeometry(0.032, 0.027, 0.072, 12);
    const coffeeGeo = new THREE.CylinderGeometry(0.027, 0.027, 0.004, 12);
    const handleGeo = new THREE.TorusGeometry(0.018, 0.005, 6, 12, Math.PI * 1.15);
    const steamGeo = new THREE.SphereGeometry(0.009, 6, 6);

    const cupMat = makeToonMat(THREE, 0xeae4d8, grad);
    const coffeeMat = makeToonMat(THREE, 0x3b2418, grad);
    const steamMat = typeof THREE.MeshBasicMaterial === "function"
      ? new THREE.MeshBasicMaterial({ color: 0xf0f4ff, transparent: true, opacity: 0.45 })
      : makeToonMat(THREE, 0xf0f4ff, grad);
    disposables.push(cupGeo, coffeeGeo, handleGeo, steamGeo, cupMat, coffeeMat, steamMat);

    addOutlinedMesh(THREE, group, cupGeo, cupMat, outlineMat, 1.08);
    const liquid = new THREE.Mesh(coffeeGeo, coffeeMat);
    liquid.position.set(0, 0.031, 0);
    group.add(liquid);

    const handle = new THREE.Mesh(handleGeo, cupMat);
    handle.position.set(-0.031, 0, 0);
    handle.rotation.z = Math.PI * 0.45;
    group.add(handle);

    const steams = [];
    for (let i = 0; i < 2; i++) {
      const puff = new THREE.Mesh(steamGeo, steamMat);
      puff.position.set(i === 0 ? -0.006 : 0.007, 0.05 + i * 0.022, 0);
      group.add(puff);
      steams.push(puff);
    }
    return { group, steams, radius: 0.055 };
  }

  function buildMailEnvelope(THREE, grad, outlineMat, disposables) {
    const group = new THREE.Group();
    group.name = "ws-prop-mail";
    group.visible = false;

    const envGeo = new THREE.BoxGeometry(0.088, 0.058, 0.010);
    const flapGeo = new THREE.ConeGeometry(0.044, 0.034, 3);
    const sealGeo = new THREE.CylinderGeometry(0.009, 0.009, 0.004, 10);

    const envMat = makeToonMat(THREE, 0xf4efe4, grad);
    const flapMat = makeToonMat(THREE, 0xe2dacb, grad);
    const sealMat = makeToonMat(THREE, 0xd94b3d, grad);
    disposables.push(envGeo, flapGeo, sealGeo, envMat, flapMat, sealMat);

    addOutlinedMesh(THREE, group, envGeo, envMat, outlineMat, 1.08);
    const flap = new THREE.Mesh(flapGeo, flapMat);
    flap.position.set(0, 0.011, 0.006);
    flap.rotation.z = Math.PI;
    flap.scale.set(1.0, 0.85, 0.2);
    group.add(flap);

    const seal = new THREE.Mesh(sealGeo, sealMat);
    seal.rotation.x = Math.PI * 0.5;
    seal.position.set(0, -0.002, 0.008);
    group.add(seal);

    return { group, radius: 0.052 };
  }

  function createController(runtime, vrm, scene, camera, opts = {}) {
    const { THREE } = runtime;
    const isVrm0 = !!opts.isVrm0;
    const framing = opts.framing || { headY: 1.45, chestY: 1.18, bustSpan: 0.27, targetY: 1.36 };
    const getSize = typeof opts.getSize === "function" ? opts.getSize : () => ({ width: 240, height: 280 });
    const hasMeshPrimitives = typeof THREE.Group === "function" && typeof THREE.BoxGeometry === "function";

    const ik = C.vrmIK?.createArmIKSolver ? C.vrmIK.createArmIKSolver(THREE, vrm, isVrm0) : null;
    const disposables = [];
    let gradMap = null, outlineMat = null;
    let props = null;

    if (hasMeshPrimitives && scene) {
      gradMap = createToonGradientMap(THREE);
      if (gradMap) disposables.push(gradMap);
      outlineMat = makeOutlineMat(THREE);
      if (outlineMat && typeof outlineMat.dispose === "function") disposables.push(outlineMat);

      props = {
        smartphone: buildSmartphone(THREE, gradMap, outlineMat, disposables),
        drone: buildDrone(THREE, gradMap, outlineMat, disposables),
        coffee: buildCoffeeMug(THREE, gradMap, outlineMat, disposables),
        mail: buildMailEnvelope(THREE, gradMap, outlineMat, disposables)
      };
      for (const item of Object.values(props)) scene.add(item.group);
    }

    let lookAtTarget = null;
    const tmpLookTarget = new THREE.Vector3();
    const tmpHead = new THREE.Vector3();
    const tmpChest = new THREE.Vector3();
    const tmpLeftTarget = new THREE.Vector3();
    const tmpRightTarget = new THREE.Vector3();
    const tmpLeftPole = new THREE.Vector3();
    const tmpRightPole = new THREE.Vector3();
    const tmpP0 = new THREE.Vector3(), tmpP1 = new THREE.Vector3(), tmpP2 = new THREE.Vector3(), tmpP3 = new THREE.Vector3();
    const tmpBezier = new THREE.Vector3();
    const tmpProj = new THREE.Vector3();

    function hideAllProps() {
      if (!props) return;
      for (const item of Object.values(props)) {
        item.group.visible = false;
        if (item.light) item.light.intensity = 0;
      }
    }

    function readBodyAnchors() {
      const headNode = vrm.humanoid?.getNormalizedBoneNode("head");
      const chestNode =
        vrm.humanoid?.getNormalizedBoneNode("upperChest") ||
        vrm.humanoid?.getNormalizedBoneNode("chest") ||
        vrm.humanoid?.getNormalizedBoneNode("spine");
      tmpHead.set(0, framing.headY, 0);
      tmpChest.set(0, framing.chestY, 0);
      if (headNode) headNode.getWorldPosition(tmpHead);
      if (chestNode) chestNode.getWorldPosition(tmpChest);
      const span = Math.max(0.18, tmpHead.y - tmpChest.y);
      return { head: tmpHead, chest: tmpChest, span };
    }

    function computeLandmarkSideBias(extra, landmarkKey) {
      const mark = extra?.marks?.[landmarkKey];
      const pos = extra?.position;
      if (!mark || !pos) return -1; // default left of dock (toward main dashboard)
      return mark.x < pos.x ? -1 : 1;
    }

    function setHandTarget(out, x, y, z, width, height, insetPx = 22) {
      out.set(x, y, z);
      return clampWorldToInset(THREE, out, camera, width, height, insetPx);
    }

    function setPropWorldPos(group, x, y, z, width, height, insetPx = 30) {
      tmpBezier.set(x, y, z);
      clampWorldToInset(THREE, tmpBezier, camera, width, height, insetPx);
      group.position.copy(tmpBezier);
    }

    function update({ dt, elapsed, pose, gesture, prop, expression, physics, extra }) {
      hideAllProps();
      lookAtTarget = null;
      if (!ik) return;

      vrm.scene.updateMatrixWorld?.(true);
      camera?.updateMatrixWorld?.(true);
      const { width, height } = getSize();
      const { head, chest, span } = readBodyAnchors();
      const progress = typeof extra?.progress === "number" ? extra.progress : ((elapsed % 2.4) / 2.4);
      const beatProp = extra?.beat?.prop;
      const propAction = typeof beatProp === "object" && beatProp ? (beatProp.action || beatProp.motion || "") : "";

      const activePropId = prop || (pose === "typing" ? "smartphone" : gesture === "coffee" ? "coffee" : null);
      const activeMode =
        activePropId === "drone" ? "drone" :
        activePropId === "mail" ? "mail" :
        activePropId === "smartphone" || pose === "typing" ? "typing" :
        activePropId === "coffee" || gesture === "coffee" ? "coffee" :
        gesture === "wave" ? "wave" :
        gesture === "point" ? "point" :
        gesture === "headrub" ? "headrub" :
        gesture === "cup_hands" ? "cup_hands" :
        gesture === "perched" || pose === "perched" ? "perched" :
        gesture === "dangle" || pose === "dangle" ? "dangle" :
        expression === "oo" ? "oo" : "stand";

      // Default relaxed poles (slightly outward, down, and behind torso).
      tmpLeftPole.set(chest.x + span * 0.92, chest.y - span * 0.35, chest.z - span * 0.55);
      tmpRightPole.set(chest.x - span * 0.92, chest.y - span * 0.35, chest.z - span * 0.55);

      let leftPreset = "relaxed";
      let rightPreset = "relaxed";
      let leftHandEuler = [0, 0, 0];
      let rightHandEuler = [0, 0, 0];
      let solveLeft = false;
      let solveRight = false;

      if (activeMode === "typing") {
        // Smartphone two-hand IK grip centered cleanly in front of upper chest.
        const tapL = Math.sin(elapsed * 11.5) * span * 0.025;
        const tapR = Math.sin(elapsed * 11.5 + 1.9) * span * 0.025;
        const phoneX = head.x;
        const phoneY = chest.y + span * 0.16;
        const phoneZ = chest.z + span * 0.72;
        if (props?.smartphone) {
          props.smartphone.group.visible = true;
          setPropWorldPos(props.smartphone.group, phoneX, phoneY, phoneZ, width, height, 34);
          props.smartphone.group.rotation.set(-0.24, Math.sin(elapsed * 1.6) * 0.04, 0);
          if (props.smartphone.light) props.smartphone.light.intensity = 0.42 + Math.sin(elapsed * 7) * 0.06;
        }
        const px = props?.smartphone ? props.smartphone.group.position.x : phoneX;
        const py = props?.smartphone ? props.smartphone.group.position.y : phoneY;
        const pz = props?.smartphone ? props.smartphone.group.position.z : phoneZ;
        setHandTarget(tmpLeftTarget, px + span * 0.16, py - span * 0.06 + tapL, pz - span * 0.02, width, height);
        setHandTarget(tmpRightTarget, px - span * 0.16, py - span * 0.06 + tapR, pz - span * 0.02, width, height);
        leftPreset = "hold_phone";
        rightPreset = "hold_phone";
        leftHandEuler = [0.22, -0.35, -0.15];
        rightHandEuler = [0.22, 0.35, 0.15];
        solveLeft = solveRight = true;
      } else if (activeMode === "drone") {
        // Subagent drone: cupped palm liftoff / return bezier arc + lookAt tracking.
        const isReturn = propAction === "return";
        const dirSign = computeLandmarkSideBias(extra, "hud-roster"); // -1 toward left of dock
        const recoil = isReturn && progress > 0.78
          ? -Math.sin(((progress - 0.78) / 0.22) * Math.PI) * span * 0.09
          : !isReturn && progress < 0.24
            ? -Math.sin((progress / 0.24) * Math.PI) * span * 0.05
            : 0;
        const palmY = chest.y + span * 0.12 + recoil;
        const palmZ = chest.z + span * 0.68;
        setHandTarget(tmpLeftTarget, head.x + span * 0.18, palmY, palmZ, width, height);
        setHandTarget(tmpRightTarget, head.x - span * 0.18, palmY, palmZ, width, height);
        leftPreset = rightPreset = "cup";
        leftHandEuler = [0.18, -0.25, -0.35];
        rightHandEuler = [0.18, 0.25, 0.35];
        solveLeft = solveRight = true;

        if (props?.drone) {
          const d = props.drone;
          d.group.visible = true;
          // 3D cubic bezier flight path kept strictly inside the bust dock frustum.
          tmpP0.set(head.x, palmY + span * 0.22, palmZ);
          tmpP1.set(head.x + dirSign * span * 0.14, head.y + span * 0.12, palmZ + span * 0.08);
          tmpP2.set(head.x + dirSign * span * 0.42, head.y + span * 0.34, palmZ + span * 0.04);
          tmpP3.set(head.x + dirSign * span * 0.52, head.y + span * 0.24, chest.z + span * 0.45);
          clampWorldToInset(THREE, tmpP0, camera, width, height, 34);
          clampWorldToInset(THREE, tmpP1, camera, width, height, 34);
          clampWorldToInset(THREE, tmpP2, camera, width, height, 34);
          clampWorldToInset(THREE, tmpP3, camera, width, height, 34);

          const u = isReturn
            ? Math.max(0, Math.min(1, 1 - Math.min(1, progress / 0.82)))
            : Math.max(0, Math.min(1, (progress - 0.14) / 0.76));
          cubicBezier3(THREE, tmpP0, tmpP1, tmpP2, tmpP3, u, tmpBezier);
          tmpBezier.y += Math.sin(elapsed * 8.5) * span * 0.025;
          setPropWorldPos(d.group, tmpBezier.x, tmpBezier.y, tmpBezier.z, width, height, 34);
          d.group.rotation.set(0.12 * Math.sin(elapsed * 4), elapsed * 1.4, -dirSign * 0.18 * Math.sin(u * Math.PI));
          const rotorSpeed = (isReturn && progress > 0.85) ? 6 : 24;
          for (let i = 0; i < d.rotors.length; i++) {
            d.rotors[i].rotation.y += dt * rotorSpeed * (i % 2 === 0 ? 1 : -1);
          }
          tmpLookTarget.copy(d.group.position);
          lookAtTarget = tmpLookTarget;
        }
      } else if (activeMode === "coffee") {
        // Right hand holds ceramic coffee mug with gentle periodic sip lift.
        const sip = Math.max(0, Math.sin(elapsed * 1.25));
        const mugX = head.x - span * 0.20;
        const mugY = chest.y + span * (0.20 + sip * 0.24);
        const mugZ = chest.z + span * 0.64;
        setHandTarget(tmpRightTarget, mugX - span * 0.06, mugY - span * 0.04, mugZ - span * 0.02, width, height);
        setHandTarget(tmpLeftTarget, head.x + span * 0.34, chest.y - span * 0.08, chest.z + span * 0.26, width, height);
        rightPreset = "cup";
        leftPreset = "relaxed";
        rightHandEuler = [0.12, 0.28, 0.14];
        solveLeft = solveRight = true;

        if (props?.coffee) {
          const mug = props.coffee;
          mug.group.visible = true;
          setPropWorldPos(mug.group, mugX, mugY, mugZ, width, height, 34);
          mug.group.rotation.set(sip * 0.22, 0.35, -0.06);
          for (let i = 0; i < mug.steams.length; i++) {
            const phase = (elapsed * 0.9 + i * 0.5) % 1;
            mug.steams[i].position.y = 0.045 + phase * 0.036;
            mug.steams[i].position.x = (i === 0 ? -0.006 : 0.007) + Math.sin(elapsed * 3 + i) * 0.005;
          }
        }
      } else if (activeMode === "mail") {
        const isBonk = propAction === "bonk-from-edge";
        const dirSign = computeLandmarkSideBias(extra, "hud-roster");
        if (isBonk) {
          // Mailbox bonk: envelope swoops gently near cheek/forehead and rebounds inside frame.
          setHandTarget(tmpLeftTarget, head.x + span * 0.34, chest.y + span * 0.04, chest.z + span * 0.30, width, height);
          setHandTarget(tmpRightTarget, head.x - span * 0.34, chest.y + span * 0.04, chest.z + span * 0.30, width, height);
          leftPreset = rightPreset = "open";
          solveLeft = solveRight = true;
          if (props?.mail) {
            const m = props.mail;
            m.group.visible = true;
            const impact = 0.45;
            if (progress <= impact) {
              const u = progress / impact;
              setPropWorldPos(m.group,
                head.x + dirSign * span * (0.56 * (1 - u) + 0.12 * u),
                head.y + span * (0.28 * (1 - u) + 0.05 * u),
                chest.z + span * 0.62,
                width, height, 34);
              m.group.rotation.set(0.1, 0.2, -0.35 * (1 - u));
            } else {
              const u = (progress - impact) / (1 - impact);
              setPropWorldPos(m.group,
                head.x + dirSign * span * (0.12 + 0.38 * u),
                head.y + span * (0.05 + 0.14 * Math.sin(u * Math.PI) - 0.32 * u),
                chest.z + span * 0.64,
                width, height, 34);
              m.group.rotation.set(0.15 * u, 0.4 * u, 0.5 * u);
            }
            tmpLookTarget.copy(m.group.position);
            lookAtTarget = tmpLookTarget;
          }
        } else {
          // Mail send: launch envelope from cupped palms.
          const palmY = chest.y + span * 0.12;
          const palmZ = chest.z + span * 0.66;
          setHandTarget(tmpLeftTarget, head.x + span * 0.18, palmY, palmZ, width, height);
          setHandTarget(tmpRightTarget, head.x - span * 0.18, palmY, palmZ, width, height);
          leftPreset = rightPreset = "cup";
          solveLeft = solveRight = true;
          if (props?.mail) {
            const m = props.mail;
            m.group.visible = true;
            tmpP0.set(head.x, palmY + span * 0.18, palmZ);
            tmpP1.set(head.x + dirSign * span * 0.16, head.y + span * 0.08, palmZ + span * 0.06);
            tmpP2.set(head.x + dirSign * span * 0.40, head.y + span * 0.28, palmZ + span * 0.02);
            tmpP3.set(head.x + dirSign * span * 0.48, head.y + span * 0.20, chest.z + span * 0.46);
            clampWorldToInset(THREE, tmpP0, camera, width, height, 34);
            clampWorldToInset(THREE, tmpP1, camera, width, height, 34);
            clampWorldToInset(THREE, tmpP2, camera, width, height, 34);
            clampWorldToInset(THREE, tmpP3, camera, width, height, 34);
            cubicBezier3(THREE, tmpP0, tmpP1, tmpP2, tmpP3, Math.max(0, Math.min(1, progress)), tmpBezier);
            setPropWorldPos(m.group, tmpBezier.x, tmpBezier.y, tmpBezier.z, width, height, 34);
            m.group.rotation.set(-0.2, 0.25 * Math.sin(elapsed * 4), dirSign * 0.22);
            tmpLookTarget.copy(m.group.position);
            lookAtTarget = tmpLookTarget;
          }
        }
      } else if (activeMode === "wave") {
        // IK-framed in-viewport wave: right hand waves beside cheek, left tucked inside frame.
        const w = Math.sin(elapsed * 6.8);
        setHandTarget(tmpRightTarget, head.x - span * (0.44 - w * 0.10), head.y - span * 0.02 + Math.abs(w) * span * 0.03, chest.z + span * 0.56, width, height, 26);
        tmpRightPole.set(chest.x - span * 0.72, chest.y - span * 0.22, chest.z + span * 0.05);
        setHandTarget(tmpLeftTarget, head.x + span * 0.34, chest.y - span * 0.08, chest.z + span * 0.26, width, height, 24);
        rightPreset = "open";
        leftPreset = "relaxed";
        rightHandEuler = [0.05, 0.12, w * 0.28];
        solveLeft = solveRight = true;
      } else if (activeMode === "point") {
        // IK-framed point: right index finger points cleanly inward-forward inside viewport.
        setHandTarget(tmpRightTarget, head.x - span * 0.34, chest.y + span * 0.42, chest.z + span * 0.66, width, height, 26);
        tmpRightPole.set(chest.x - span * 0.76, chest.y - span * 0.12, chest.z - span * 0.10);
        setHandTarget(tmpLeftTarget, head.x + span * 0.34, chest.y - span * 0.08, chest.z + span * 0.24, width, height, 24);
        rightPreset = "point";
        leftPreset = "relaxed";
        rightHandEuler = [0.06, 0.18, 0.10];
        solveLeft = solveRight = true;
      } else if (activeMode === "headrub") {
        // Right hand gently rubs right temple/crown; left arm relaxed in-frame.
        const rub = Math.sin(elapsed * 4.8);
        setHandTarget(tmpRightTarget, head.x - span * (0.28 + rub * 0.04), head.y + span * (0.12 + Math.cos(elapsed * 4.8) * 0.03), chest.z + span * 0.34, width, height, 24);
        tmpRightPole.set(chest.x - span * 0.78, chest.y + span * 0.12, chest.z + span * 0.06);
        setHandTarget(tmpLeftTarget, head.x + span * 0.34, chest.y - span * 0.08, chest.z + span * 0.24, width, height, 24);
        rightPreset = "open";
        leftPreset = "relaxed";
        rightHandEuler = [0.16, 0.22, 0.25];
        solveLeft = solveRight = true;
      } else if (activeMode === "cup_hands") {
        setHandTarget(tmpLeftTarget, head.x + span * 0.18, chest.y + span * 0.12, chest.z + span * 0.64, width, height, 24);
        setHandTarget(tmpRightTarget, head.x - span * 0.18, chest.y + span * 0.12, chest.z + span * 0.64, width, height, 24);
        leftPreset = rightPreset = "cup";
        leftHandEuler = [0.16, -0.24, -0.32];
        rightHandEuler = [0.16, 0.24, 0.32];
        solveLeft = solveRight = true;
      } else if (activeMode === "perched") {
        const sway = Math.sin(elapsed * 1.1) * span * 0.02;
        setHandTarget(tmpLeftTarget, head.x + span * 0.24 + sway, chest.y - span * 0.04, chest.z + span * 0.52, width, height, 24);
        setHandTarget(tmpRightTarget, head.x - span * 0.24 + sway, chest.y - span * 0.04, chest.z + span * 0.52, width, height, 24);
        leftPreset = rightPreset = "relaxed";
        solveLeft = solveRight = true;
      } else if (activeMode === "dangle") {
        // Spring pendulum drag pose driven by stage physics.swing.
        const swing = Number.isFinite(physics?.swing) ? physics.swing : Math.sin(elapsed * 3.2) * 0.18;
        setHandTarget(tmpLeftTarget, head.x + span * (0.34 + swing * 0.35), chest.y - span * 0.06, chest.z + span * 0.28, width, height, 24);
        setHandTarget(tmpRightTarget, head.x - span * (0.34 - swing * 0.35), chest.y - span * 0.06, chest.z + span * 0.28, width, height, 24);
        leftPreset = rightPreset = "open";
        solveLeft = solveRight = true;
      } else if (activeMode === "oo") {
        // Alert/startled hands slightly raised near collar inside the bust frame.
        setHandTarget(tmpLeftTarget, head.x + span * 0.32, chest.y + span * 0.10, chest.z + span * 0.44, width, height, 24);
        setHandTarget(tmpRightTarget, head.x - span * 0.32, chest.y + span * 0.10, chest.z + span * 0.44, width, height, 24);
        leftPreset = rightPreset = "open";
        solveLeft = solveRight = true;
      }

      if (solveLeft) {
        ik.solveArm("left", tmpLeftTarget, tmpLeftPole, { dt, handEuler: leftHandEuler });
      }
      if (solveRight) {
        ik.solveArm("right", tmpRightTarget, tmpRightPole, { dt, handEuler: rightHandEuler });
      }
      ik.applyFingerPreset("left", leftPreset, dt);
      ik.applyFingerPreset("right", rightPreset, dt);
    }

    function getLookAtWorldTarget() {
      return lookAtTarget;
    }

    function projectHandBox(side, width, height) {
      const node = vrm.humanoid?.getNormalizedBoneNode(side + "Hand");
      if (!node || !camera) return null;
      const p = new THREE.Vector3();
      node.getWorldPosition(p);
      const r = 0.022;
      let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
      for (const dx of [-r, r]) {
        for (const dy of [-r, r]) {
          for (const dz of [-r, r]) {
            const pt = projectPointPx(THREE, tmpProj.set(p.x + dx, p.y + dy, p.z + dz), camera, width, height, new THREE.Vector3());
            if (pt.x < minX) minX = pt.x;
            if (pt.y < minY) minY = pt.y;
            if (pt.x > maxX) maxX = pt.x;
            if (pt.y > maxY) maxY = pt.y;
          }
        }
      }
      return {
        minX: Number(minX.toFixed(1)),
        minY: Number(minY.toFixed(1)),
        maxX: Number(maxX.toFixed(1)),
        maxY: Number(maxY.toFixed(1))
      };
    }

    function projectPropBox(group, width, height) {
      if (!group?.visible || !camera || typeof THREE.Box3 !== "function") return null;
      const box = new THREE.Box3().setFromObject(group);
      if (box.isEmpty()) return null;
      const xs = [box.min.x, box.max.x];
      const ys = [box.min.y, box.max.y];
      const zs = [box.min.z, box.max.z];
      let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
      for (const x of xs) {
        for (const y of ys) {
          for (const z of zs) {
            const pt = projectPointPx(THREE, tmpProj.set(x, y, z), camera, width, height, new THREE.Vector3());
            if (pt.x < minX) minX = pt.x;
            if (pt.y < minY) minY = pt.y;
            if (pt.x > maxX) maxX = pt.x;
            if (pt.y > maxY) maxY = pt.y;
          }
        }
      }
      return {
        minX: Number(minX.toFixed(1)),
        minY: Number(minY.toFixed(1)),
        maxX: Number(maxX.toFixed(1)),
        maxY: Number(maxY.toFixed(1))
      };
    }

    function getProjectedBounds() {
      if (!vrm || !camera || typeof tmpProj.project !== "function") return null;
      vrm.scene.updateMatrixWorld?.(true);
      camera.updateMatrixWorld?.(true);
      const { width, height } = getSize();
      const leftHand = projectHandBox("left", width, height);
      const rightHand = projectHandBox("right", width, height);
      const activeProps = Object.create(null);
      if (props) {
        for (const [name, item] of Object.entries(props)) {
          if (item.group.visible) {
            item.group.updateMatrixWorld?.(true);
            const b = projectPropBox(item.group, width, height);
            if (b) activeProps[name] = b;
          }
        }
      }
      const withinInset4 = box => !box || (box.minX >= 4 && box.minY >= 4 && box.maxX <= width - 4 && box.maxY <= height - 4);
      let allWithinInset4 = withinInset4(leftHand) && withinInset4(rightHand);
      for (const b of Object.values(activeProps)) {
        if (!withinInset4(b)) allWithinInset4 = false;
      }
      return { width, height, leftHand, rightHand, props: activeProps, allWithinInset4 };
    }

    function resetIK() {
      ik?.syncFromCurrentBones();
    }

    function dispose() {
      if (props && scene) {
        for (const item of Object.values(props)) {
          scene.remove(item.group);
        }
      }
      props = null;
      for (const d of disposables) {
        try { d?.dispose?.(); } catch (_) { /* ignore */ }
      }
      disposables.length = 0;
    }

    return {
      update,
      getLookAtWorldTarget,
      getProjectedBounds,
      resetIK,
      dispose
    };
  }

  C.vrmProps = {
    enableVrma: true,
    poses: VRM_POSES,
    gestures: VRM_GESTURES,
    props: VRM_PROPS,
    hasPose: pose => typeof pose === "string" && Object.hasOwn(VRM_POSES, pose),
    createController
  };
})(window.WsCompanion);
