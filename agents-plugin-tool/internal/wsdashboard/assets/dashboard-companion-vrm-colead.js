(function (C) {
  "use strict";

  // ============================================================================
  // Glass-Wall Co-Lead Choreography State Machine
  // ============================================================================
  // Orchestrates typing/reading/glance-up rhythm, session-switch laptop swaps,
  // rapid-switch dizzy easter egg, cosmetic glass knocks, smartphone states,
  // and breakout activity flags. Visual props live in dashboard-companion-vrm-props.js,
  // FK pose math lives in dashboard-companion-vrm-pose.js, and 2D FX live in
  // dashboard-companion-vrm-fx.js.

  const DEG = Math.PI / 180;
  const Props = C.vrmCoLeadProps;
  const Pose = C.vrmCoLeadPose;

  const {
    DEFAULT_ACCENT,
    SESSION_ACCENT_PALETTE,
    normalizeHexColor,
    sessionAccentForKey,
    findPaletteEntryByHex,
    createCoLeadProps
  } = Props;

  const {
    clamp,
    dampAlpha,
    damp,
    hashPhase,
    mirrorLR,
    classifyActivity,
    computeCoLeadTargets,
    evalDizzyBeat,
    evalSwapBeat,
    evalKnockBeat,
    evalPhoneBeat,
    evalPhonePropTarget
  } = Pose;

  const VALID_BEATS = new Set([
    "auto", "typing", "glance-up", "working", "sleepy", "reading-left", "reading-right",
    "swap-out", "swap-in", "dizzy", "dizzy-shake", "dizzy-recover",
    "knock-startle", "annoyed-tap", "wakeup-jolt",
    "phone-buzz", "phone-pickup", "phone-typing", "phone-putdown", "phone-facedown-merong"
  ]);

  const SWAP_OUT_DURATION = 0.30;
  const SWAP_IN_DURATION = 0.35;
  const SWAP_SETTLE_DURATION = 0.20;

  const DIZZY_WINDOW_SEC = 2.8;
  const DIZZY_TRIGGER_COUNT = 4;
  const DIZZY_SWAY_DURATION = 2.85;
  const DIZZY_RECOVER_DURATION = 0.75;
  const DIZZY_TOTAL_DURATION = DIZZY_SWAY_DURATION + DIZZY_RECOVER_DURATION;
  const DIZZY_COOLDOWN_SEC = 1.2;

  const KNOCK_WINDOW_SEC = 4.0;
  const KNOCK_ANNOYED_COUNT = 3;
  const KNOCK_STARTLE_DURATION = 1.05;
  const KNOCK_WAKEUP_DURATION = 1.35;
  const KNOCK_ANNOYED_DURATION = 1.85;
  const PHONE_PUTDOWN_DURATION = 0.72;
  const PHONE_MERONG_DURATION = 1.75;

  function workingWOffset(w) {
    return w * 1.1;
  }

  function createVrmCoLead(opts = {}) {
    const props = createCoLeadProps(opts);

    let accentHex = DEFAULT_ACCENT;
    let targetAccentHex = DEFAULT_ACCENT;
    let accentId = "sapphire";
    let activeSessionId = "";
    let activeSessionMeta = null;
    let overrideBeat = "auto";
    let explicitHover = null;

    // Monotonic clock bridging simulated step(dt) and live browser wall time.
    let simClockSec = 0;
    let lastStepWallMs = 0;
    let lastObservedSec = 0;

    // Rhythm state machine.
    let phase = "typing"; // 'typing' | 'reading' | 'glance-up'
    let phaseTimer = 2.1;
    let phaseCycle = 0;
    let timeSinceGlance = 0;
    let nextGlanceInterval = 8.4;
    let glanceTiltDir = 1;
    let lastActivity = "idle";

    // Session-switch laptop swap choreography (~0.85s, smoothly interruptible).
    let swapActive = false;
    let swapElapsed = 0;
    let swapOutDuration = SWAP_OUT_DURATION;
    let swapDir = 1;
    let swapSwitchedColor = false;
    let swapStartPose = { dx: 0, dy: 0, dz: 0, rx: 0, ry: 0, rz: 0 };

    // Rapid-switch sliding-window dizzy easter egg (>=4 distinct switches within 2.8s).
    const switchTimestamps = [];
    let dizzyPhase = "none"; // 'none' | 'sway' | 'recover'
    let dizzyElapsed = 0;
    let dizzyCooldownUntil = -Infinity;
    let dizzyFxAlpha = 0;

    // Cosmetic glass knock state machine.
    const knockTimestamps = [];
    let knockKind = ""; // '' | 'knock-startle' | 'wakeup-jolt' | 'annoyed-tap'
    let knockElapsed = 0;
    let knockDuration = 0;
    let knockCount = 0;

    // Smartphone 3D prop & 4-state choreography state.
    let phoneMode = "desk"; // 'desk' | 'buzz' | 'pickup' | 'typing' | 'putdown' | 'facedown-merong'
    let phoneFaceDown = false;
    let phoneAnimElapsed = 0;
    let phoneUnreadAgent = 0;
    let phoneLastSnapState = "laptop_ambient";
    // Server-confirmed hold: null until the first snapshot, then whether the
    // latest snapshot said the phone is held. A pickup/typing pose the server
    // does not confirm within PHONE_UNCONFIRMED_GRACE is put back down, so a
    // lost putdown command can never strand the phone in hand.
    let phoneSnapHeld = null;
    let phoneUnconfirmedElapsed = 0;
    const PHONE_UNCONFIRMED_GRACE = 4;
    let phoneHoldWeight = 0;
    let phoneTypingWeight = 0;
    let phoneMerongWeight = 0;
    let knockJoltWeight = 0;
    let annoyedTapWeight = 0;
    let actionFxKind = "";
    let actionFxAlpha = 0;

    // Metafiction breakout state hook (driven by VRM rig render mode & Phase 3 choreography).
    let breakoutActive = false;
    let breakoutMode = "docked"; // 'docked' | 'bleed' | 'roam'

    // Smoothly damped blending weights (frame-rate independent).
    let typingWeight = 1.0;
    let glanceWeight = 0.0;
    let sleepyWeight = 0.0;
    let reactionWeight = 0.0;
    let workingWeight = 0.0;
    let swapWeight = 0.0;
    let dizzyWeight = 0.0;

    function readWallMs() {
      if (typeof performance !== "undefined" && typeof performance.now === "function") {
        const ms = performance.now();
        if (Number.isFinite(ms) && ms > 0) return ms;
      }
      return 0;
    }

    function currentClockSec(overrideMs) {
      if (Number.isFinite(overrideMs)) {
        const sec = overrideMs / 1000;
        lastObservedSec = Math.max(lastObservedSec, sec);
        return lastObservedSec;
      }
      const wallMs = readWallMs();
      let wallDelta = 0;
      if (wallMs > 0 && lastStepWallMs > 0 && wallMs > lastStepWallMs) {
        wallDelta = Math.min((wallMs - lastStepWallMs) / 1000, 5.0);
      } else if (wallMs > 0 && lastStepWallMs === 0 && simClockSec === 0) {
        lastStepWallMs = wallMs;
      }
      const next = simClockSec + wallDelta;
      lastObservedSec = Math.max(lastObservedSec, next);
      return lastObservedSec;
    }

    function pruneSwitchWindow(nowSec) {
      while (switchTimestamps.length && nowSec - switchTimestamps[0] > DIZZY_WINDOW_SEC) {
        switchTimestamps.shift();
      }
    }

    function layoutProp(framing, camOverride) {
      props.layoutProp(framing, camOverride, { accentHex, phoneFaceDown });
    }

    const fingerScratch = {};
    function syncPhoneToHand(palmProbeOrHandPos) {
      const input = typeof palmProbeOrHandPos === "function" ? (phoneHoldWeight > 0.01 ? palmProbeOrHandPos() : null) : palmProbeOrHandPos;
      props.syncPhoneToHand(input, phoneHoldWeight);
    }

    function advanceRhythm(dt, activity, hovered) {
      if (activity !== lastActivity) {
        if (activity === "working") {
          phase = "typing";
          phaseTimer = 2.4;
        } else if (activity === "idle" && lastActivity !== "idle") {
          phase = "reading";
          phaseTimer = 1.6;
        }
        lastActivity = activity;
      }

      if (overrideBeat !== "auto") {
        if (overrideBeat === "glance-up") phase = "glance-up";
        else if (overrideBeat === "typing" || overrideBeat === "working") phase = "typing";
        else phase = "reading";
        return;
      }

      if (activity === "sleepy" || activity === "reaction") {
        phase = "reading";
        timeSinceGlance = 0;
        return;
      }

      if (activity === "working") {
        timeSinceGlance = 0;
        phaseTimer -= dt;
        if (phaseTimer <= 0) {
          phaseCycle++;
          const r = hashPhase(phaseCycle);
          if (phase === "typing") {
            phase = "reading";
            phaseTimer = 0.55 + r * 0.60;
          } else {
            phase = "typing";
            phaseTimer = 1.85 + r * 1.55;
          }
        }
        return;
      }

      if (hovered) {
        if (phase !== "glance-up") glanceTiltDir = -glanceTiltDir;
        phase = "glance-up";
        timeSinceGlance = 0;
        phaseTimer = Math.max(phaseTimer, 0.45);
        return;
      }

      if (phase === "glance-up") {
        phaseTimer -= dt;
        if (phaseTimer <= 0) {
          phaseCycle++;
          phase = "typing";
          phaseTimer = 1.4 + hashPhase(phaseCycle) * 1.0;
          timeSinceGlance = 0;
          nextGlanceInterval = 7.4 + hashPhase(phaseCycle + 17) * 3.2;
        }
        return;
      }

      timeSinceGlance += dt;
      if (timeSinceGlance >= nextGlanceInterval) {
        phaseCycle++;
        glanceTiltDir = -glanceTiltDir;
        phase = "glance-up";
        phaseTimer = 2.4 + hashPhase(phaseCycle + 31) * 0.55;
        timeSinceGlance = 0;
        return;
      }

      phaseTimer -= dt;
      if (phaseTimer <= 0) {
        phaseCycle++;
        const r = hashPhase(phaseCycle);
        if (phase === "typing") {
          phase = "reading";
          phaseTimer = 2.5 + r * 2.1;
        } else {
          phase = "typing";
          phaseTimer = 1.25 + r * 1.15;
        }
      }
    }

    function step(ctx) {
      const dt = Number.isFinite(ctx.dt) && ctx.dt > 0 ? Math.min(ctx.dt, 0.2) : 0.016;
      const elapsed = Number.isFinite(ctx.elapsed) ? ctx.elapsed : 0;
      const hovered = explicitHover !== null ? explicitHover : !!ctx.hover;

      simClockSec += dt;
      const wallMs = readWallMs();
      if (wallMs > 0) lastStepWallMs = wallMs;
      lastObservedSec = Math.max(lastObservedSec, simClockSec);
      pruneSwitchWindow(lastObservedSec);

      let activity = classifyActivity(ctx);
      if (overrideBeat === "working") activity = "working";
      else if (overrideBeat === "sleepy") activity = "sleepy";
      else if (
        overrideBeat === "typing" || overrideBeat === "glance-up" ||
        overrideBeat === "reading-left" || overrideBeat === "reading-right" ||
        overrideBeat === "swap-out" || overrideBeat === "swap-in" ||
        overrideBeat === "dizzy" || overrideBeat === "dizzy-shake" || overrideBeat === "dizzy-recover" ||
        overrideBeat === "knock-startle" || overrideBeat === "annoyed-tap" || overrideBeat === "wakeup-jolt" ||
        overrideBeat === "phone-buzz" || overrideBeat === "phone-pickup" || overrideBeat === "phone-typing" ||
        overrideBeat === "phone-putdown" || overrideBeat === "phone-facedown-merong"
      ) {
        activity = "idle";
      }

      if (overrideBeat === "auto") {
        if (knockKind) {
          knockElapsed += dt;
          if (knockElapsed >= knockDuration) {
            knockKind = "";
            knockElapsed = 0;
            knockDuration = 0;
          }
        }
        if (phoneMode === "pickup" || phoneMode === "putdown" || phoneMode === "facedown-merong") {
          phoneAnimElapsed += dt;
          if (phoneMode === "pickup" && phoneAnimElapsed >= 0.45) {
            phoneMode = "typing";
            phoneAnimElapsed = 0;
          } else if (phoneMode === "putdown" && phoneAnimElapsed >= PHONE_PUTDOWN_DURATION) {
            phoneMode = phoneUnreadAgent > 0 ? "buzz" : "desk";
            phoneAnimElapsed = 0;
          } else if (phoneMode === "facedown-merong" && phoneAnimElapsed >= PHONE_MERONG_DURATION) {
            phoneMode = phoneUnreadAgent > 0 ? "buzz" : "desk";
            phoneAnimElapsed = 0;
          }
        }
        if (phoneSnapHeld === false && (phoneMode === "pickup" || phoneMode === "typing")) {
          phoneUnconfirmedElapsed += dt;
          if (phoneUnconfirmedElapsed >= PHONE_UNCONFIRMED_GRACE) {
            phoneMode = phoneFaceDown ? "facedown-merong" : "putdown";
            phoneAnimElapsed = 0;
            phoneUnconfirmedElapsed = 0;
          }
        } else {
          phoneUnconfirmedElapsed = 0;
        }
        if (dizzyPhase !== "none") {
          dizzyElapsed += dt;
          if (dizzyPhase === "sway" && dizzyElapsed >= DIZZY_SWAY_DURATION) {
            dizzyPhase = "recover";
            dizzyElapsed = 0;
            if (accentHex !== targetAccentHex) {
              accentHex = targetAccentHex;
              props.applyAccentVisuals(accentHex, false);
            }
          } else if (dizzyPhase === "recover" && dizzyElapsed >= DIZZY_RECOVER_DURATION) {
            dizzyPhase = "none";
            dizzyElapsed = 0;
            dizzyCooldownUntil = lastObservedSec + DIZZY_COOLDOWN_SEC;
            phase = "typing";
            phaseTimer = 1.8;
          }
        } else if (swapActive) {
          swapElapsed += dt;
          if (!swapSwitchedColor && swapElapsed >= swapOutDuration) {
            swapSwitchedColor = true;
            accentHex = targetAccentHex;
            props.applyAccentVisuals(accentHex, false);
          }
          const totalDur = swapOutDuration + SWAP_IN_DURATION + SWAP_SETTLE_DURATION;
          if (swapElapsed >= totalDur) {
            swapActive = false;
            swapElapsed = 0;
            swapOutDuration = SWAP_OUT_DURATION;
            phase = "typing";
            phaseTimer = 1.8;
          }
        }
      }

      const forcedSwapOut = overrideBeat === "swap-out";
      const forcedSwapIn = overrideBeat === "swap-in";
      const forcedDizzy = overrideBeat === "dizzy";
      const forcedDizzyShake = overrideBeat === "dizzy-shake";
      const forcedDizzyRecover = overrideBeat === "dizzy-recover";
      const forcedKnockStartle = overrideBeat === "knock-startle";
      const forcedWakeupJolt = overrideBeat === "wakeup-jolt";
      const forcedAnnoyedTap = overrideBeat === "annoyed-tap";
      const forcedPhoneBuzz = overrideBeat === "phone-buzz";
      const forcedPhonePickup = overrideBeat === "phone-pickup";
      const forcedPhoneTyping = overrideBeat === "phone-typing";
      const forcedPhonePutdown = overrideBeat === "phone-putdown";
      const forcedPhoneMerong = overrideBeat === "phone-facedown-merong";

      const inDizzySway = forcedDizzy || (overrideBeat === "auto" && dizzyPhase === "sway");
      const inDizzyShake = forcedDizzyShake || (overrideBeat === "auto" && dizzyPhase === "recover" && dizzyElapsed < DIZZY_RECOVER_DURATION * 0.48);
      const inDizzyRecover = forcedDizzyRecover || (overrideBeat === "auto" && dizzyPhase === "recover" && dizzyElapsed >= DIZZY_RECOVER_DURATION * 0.48);
      const inAnyDizzy = inDizzySway || inDizzyShake || inDizzyRecover;

      const activeKnockKind = forcedKnockStartle ? "knock-startle"
        : forcedWakeupJolt ? "wakeup-jolt"
          : forcedAnnoyedTap ? "annoyed-tap"
            : (overrideBeat === "auto" ? knockKind : "");
      const inAnyKnock = !inAnyDizzy && !!activeKnockKind;

      const activePhoneStage = forcedPhoneBuzz ? "buzz"
        : forcedPhonePickup ? "pickup"
          : forcedPhoneTyping ? "typing"
            : forcedPhonePutdown ? "putdown"
              : forcedPhoneMerong ? "facedown-merong"
                : (overrideBeat === "auto" ? phoneMode : "desk");
      const inPhoneAction = !inAnyDizzy && !inAnyKnock && activePhoneStage !== "desk";
      const inSwap = !inAnyDizzy && !inAnyKnock && !inPhoneAction && (forcedSwapOut || forcedSwapIn || (overrideBeat === "auto" && swapActive));

      if (!inAnyDizzy && !inAnyKnock && !inPhoneAction && !inSwap) {
        advanceRhythm(dt, activity, hovered);
      }

      const wantPhoneHold = !inAnyDizzy && !inAnyKnock && (
        activePhoneStage === "pickup" || activePhoneStage === "typing" ||
        (activePhoneStage === "facedown-merong" && !forcedPhoneMerong && phoneAnimElapsed < PHONE_MERONG_DURATION * 0.48)
      );
      const wantPhoneTyping = !inAnyDizzy && !inAnyKnock && activePhoneStage === "typing";
      const wantPhoneMerong = !inAnyDizzy && !inAnyKnock && activePhoneStage === "facedown-merong";
      const wantKnockJolt = activeKnockKind === "knock-startle" || activeKnockKind === "wakeup-jolt";
      const wantAnnoyedTap = activeKnockKind === "annoyed-tap";

      const wantGlance = !inAnyDizzy && !inAnyKnock && !inPhoneAction && !inSwap && (overrideBeat === "glance-up" || (overrideBeat === "auto" && activity === "idle" && (hovered || phase === "glance-up")));
      const wantSleepy = !inAnyDizzy && !inAnyKnock && !inPhoneAction && !inSwap && activity === "sleepy";
      const wantReaction = !inAnyDizzy && !inAnyKnock && !inPhoneAction && !inSwap && activity === "reaction";
      const wantWorking = !inAnyDizzy && !inAnyKnock && !inPhoneAction && !inSwap && activity === "working";
      const wantTyping = !inAnyDizzy && !inAnyKnock && !inPhoneAction && !inSwap && !wantGlance && !wantSleepy && !wantReaction && (
        overrideBeat === "typing" ||
        overrideBeat === "working" ||
        (overrideBeat === "auto" && phase === "typing")
      );

      typingWeight = damp(typingWeight, wantTyping ? 1 : 0, wantTyping ? 6.5 : 10.0, dt);
      glanceWeight = damp(glanceWeight, wantGlance ? 1 : 0, wantGlance ? 5.8 : 5.2, dt);
      sleepyWeight = damp(sleepyWeight, wantSleepy ? 1 : 0, 3.6, dt);
      reactionWeight = damp(reactionWeight, wantReaction ? 1 : 0, 8.0, dt);
      workingWeight = damp(workingWeight, wantWorking ? 1 : 0, 5.2, dt);
      swapWeight = damp(swapWeight, inSwap ? 1 : 0, inSwap ? 11.0 : 8.5, dt);
      dizzyWeight = damp(dizzyWeight, inDizzySway ? 1 : 0, inDizzySway ? 7.5 : 9.0, dt);
      phoneHoldWeight = damp(phoneHoldWeight, wantPhoneHold ? 1 : 0, wantPhoneHold ? 8.5 : 9.5, dt);
      phoneTypingWeight = damp(phoneTypingWeight, wantPhoneTyping ? 1 : 0, 8.0, dt);
      phoneMerongWeight = damp(phoneMerongWeight, wantPhoneMerong ? 1 : 0, 9.5, dt);
      knockJoltWeight = damp(knockJoltWeight, wantKnockJolt ? 1 : 0, wantKnockJolt ? 16.0 : 9.0, dt);
      annoyedTapWeight = damp(annoyedTapWeight, wantAnnoyedTap ? 1 : 0, 10.0, dt);

      const gx = clamp(Number.isFinite(ctx.gazeX) ? ctx.gazeX : 0, -1, 1);
      const gy = clamp(Number.isFinite(ctx.gazeY) ? ctx.gazeY : 0, -1, 1);

      let readingScan = Math.sin(elapsed * (wantWorking ? 1.55 : 0.88)) * (wantWorking ? 0.58 : 0.42) +
        Math.sin(elapsed * (wantWorking ? 2.8 : 1.45) + 1.1) * 0.18;
      if (overrideBeat === "reading-left") readingScan = -0.82;
      else if (overrideBeat === "reading-right") readingScan = 0.82;

      const screenYaw = readingScan * 8.6 * DEG + gx * 3.5 * DEG;
      const screenPitch = (5.6 + workingWOffset(workingWeight)) * DEG + gy * 1.8 * DEG;
      const glanceYaw = gx * 4.5 * DEG;
      const glancePitch = -1.8 * DEG + gy * 2.2 * DEG;
      const reactionYaw = gx * 11 * DEG;
      const reactionPitch = gy * 7 * DEG;

      let wantHeadYaw = screenYaw * (1 - glanceWeight) + glanceYaw * glanceWeight;
      let wantHeadPitch = screenPitch * (1 - glanceWeight) + glancePitch * glanceWeight;
      let extraHeadRoll = 0;
      let swapSweep = 0;
      if (reactionWeight > 0.01) {
        wantHeadYaw = wantHeadYaw * (1 - reactionWeight) + reactionYaw * reactionWeight;
        wantHeadPitch = wantHeadPitch * (1 - reactionWeight) + reactionPitch * reactionWeight;
      }
      if (sleepyWeight > 0.01) {
        wantHeadYaw *= (1 - sleepyWeight * 0.82);
        wantHeadPitch *= (1 - sleepyWeight * 0.85);
      }

      const eyeYawScreen = readingScan * 13.0 * DEG + gx * 6 * DEG;
      const eyePitchScreen = -9.5 * DEG;
      const eyeYawGlance = gx * 8 * DEG;
      const eyePitchGlance = 1.5 * DEG - gy * 4 * DEG;
      const eyeYawReaction = gx * 22 * DEG;
      const eyePitchReaction = -gy * 14 * DEG;

      let eyeYaw = eyeYawScreen * (1 - glanceWeight) + eyeYawGlance * glanceWeight;
      let eyePitch = eyePitchScreen * (1 - glanceWeight) + eyePitchGlance * glanceWeight;
      if (reactionWeight > 0.01) {
        eyeYaw = eyeYaw * (1 - reactionWeight) + eyeYawReaction * reactionWeight;
        eyePitch = eyePitch * (1 - reactionWeight) + eyePitchReaction * reactionWeight;
      }
      if (sleepyWeight > 0.01) {
        eyeYaw *= (1 - sleepyWeight * 0.85);
        eyePitch = eyePitch * (1 - sleepyWeight) + (-12 * DEG) * sleepyWeight;
      }

      const pm = props.getMetrics();
      const { basePos, baseScale, baseVisH, baseVisHalfW, phoneBasePos, lidTopWorldY } = pm;

      let targetOffset = { dx: 0, dy: 0, dz: 0, rx: 0, ry: 0, rz: 0 };
      let immediateOffset = false;
      let swapStage = "none";
      let swapProgress = 0;
      let dizzyTime = elapsed;
      let targetDizzyFxAlpha = 0;
      let exprOverlay = null;
      let glowMultiplier = 1.0;

      if (inAnyDizzy) {
        const res = evalDizzyBeat({
          inDizzySway, forcedDizzy, forcedDizzyShake, forcedDizzyRecover,
          dizzyElapsed, recoverDur: DIZZY_RECOVER_DURATION, elapsed, gx, baseScale, baseVisH
        });
        dizzyTime = res.dizzyTime;
        wantHeadYaw = res.wantHeadYaw;
        wantHeadPitch = res.wantHeadPitch;
        extraHeadRoll = res.extraHeadRoll;
        eyeYaw = res.eyeYaw;
        eyePitch = res.eyePitch;
        targetOffset = res.targetOffset;
        targetDizzyFxAlpha = res.targetDizzyFxAlpha;
        glowMultiplier = res.glowMultiplier;
        exprOverlay = res.exprOverlay;
      } else if (inSwap) {
        const outDur = forcedSwapOut || forcedSwapIn ? SWAP_OUT_DURATION : swapOutDuration;
        const res = evalSwapBeat({
          forcedSwapOut, forcedSwapIn, outDur, inDur: SWAP_IN_DURATION, settleDur: SWAP_SETTLE_DURATION,
          swapElapsed, swapDir, swapStartPose, gx, baseScale, baseVisH
        });
        swapStage = res.swapStage;
        swapProgress = res.swapProgress;
        targetOffset = res.targetOffset;
        immediateOffset = res.immediateOffset;
        swapSweep = res.swapSweep;
        wantHeadYaw = res.wantHeadYaw;
        wantHeadPitch = res.wantHeadPitch;
        extraHeadRoll = res.extraHeadRoll;
        eyeYaw = res.eyeYaw;
        eyePitch = res.eyePitch;
        glowMultiplier = res.glowMultiplier;
        exprOverlay = res.exprOverlay;
      }

      let targetActionFxKind = "";
      let targetActionFxAlpha = 0;

      if (inAnyKnock) {
        const res = evalKnockBeat({
          activeKnockKind, forcedWakeupJolt, forcedAnnoyedTap, forcedKnockStartle,
          knockElapsed, knockDuration, gx
        });
        wantHeadYaw = res.wantHeadYaw;
        wantHeadPitch = res.wantHeadPitch;
        extraHeadRoll = res.extraHeadRoll;
        eyeYaw = res.eyeYaw;
        eyePitch = res.eyePitch;
        exprOverlay = res.exprOverlay;
        targetActionFxKind = res.targetActionFxKind;
        targetActionFxAlpha = res.targetActionFxAlpha;
      } else if (inPhoneAction) {
        const res = evalPhoneBeat({ activePhoneStage, baseScale, baseVisH });
        if (res) {
          if (res.targetOffset) targetOffset = res.targetOffset;
          wantHeadYaw = res.wantHeadYaw;
          wantHeadPitch = res.wantHeadPitch;
          extraHeadRoll = res.extraHeadRoll;
          eyeYaw = res.eyeYaw;
          eyePitch = res.eyePitch;
          exprOverlay = res.exprOverlay;
          if (res.targetActionFxKind) targetActionFxKind = res.targetActionFxKind;
          if (res.targetActionFxAlpha !== undefined) targetActionFxAlpha = res.targetActionFxAlpha;
        }
      }

      const phoneIsFacedownNow = forcedPhoneMerong
        ? true
        : (activePhoneStage === "facedown-merong" && phoneAnimElapsed >= PHONE_MERONG_DURATION * 0.48) ||
          (activePhoneStage === "desk" && phoneFaceDown);
      const { phoneTarget, targetPhoneGlow, targetBuzzRingAlpha } = evalPhonePropTarget({
        activePhoneStage, wantPhoneHold, wantPhoneTyping, phoneIsFacedownNow,
        elapsed, basePos, baseScale, baseVisH, baseVisHalfW, phoneBasePos, lidTopWorldY
      });

      const keyFlicker = (Math.sin(elapsed * 9.3) * 0.5 + Math.sin(elapsed * 15.7 + 1.2) * 0.5) * 0.016 * typingWeight;
      let targetGlow = 0.13 + keyFlicker * 0.5;
      if (wantSleepy) targetGlow = 0.03;
      else if (wantWorking) targetGlow = 0.24 + keyFlicker;
      else if (wantGlance) targetGlow = 0.12;
      else if (wantReaction) targetGlow = 0.11;
      targetGlow *= glowMultiplier;

      const emblemTarget = wantSleepy ? 0.26 : (inDizzySway ? 0.42 : (wantWorking ? 0.96 : 0.88));

      props.updateVisuals({
        dt,
        elapsed,
        accentHex,
        targetOffset,
        immediateOffset,
        phoneTarget,
        targetPhoneGlow,
        targetBuzzRingAlpha,
        targetGlow,
        emblemTarget
      });

      if (targetActionFxKind) actionFxKind = targetActionFxKind;
      actionFxAlpha = damp(actionFxAlpha, targetActionFxAlpha, targetActionFxAlpha > actionFxAlpha ? 14.0 : 10.0, dt);
      if (actionFxAlpha < 0.01) {
        actionFxAlpha = 0;
        if (!targetActionFxKind) actionFxKind = "";
      }

      dizzyFxAlpha = forcedDizzy ? 1.0 : damp(dizzyFxAlpha, targetDizzyFxAlpha, targetDizzyFxAlpha > dizzyFxAlpha ? 10.0 : 12.0, dt);
      if (dizzyFxAlpha < 0.002) dizzyFxAlpha = 0;

      if (!exprOverlay) {
        if (overrideBeat === "sleepy") {
          exprOverlay = { mode: "sleepy", blink: 0.92, relaxed: 0.44, happy: 0.0 };
        } else if (activity === "idle" || activity === "working") {
          const baseHappy = activity === "working" ? 0.08 : 0.15;
          const baseRelaxed = activity === "working" ? 0.06 : 0.08;
          exprOverlay = {
            mode: wantGlance ? "glance-up" : activity,
            happy: baseHappy * (1 - glanceWeight) + 0.42 * glanceWeight,
            relaxed: baseRelaxed * (1 - glanceWeight) + 0.14 * glanceWeight,
            allowBlink: true
          };
        }
      }

      const boneTargets = computeCoLeadTargets({
        elapsed,
        typingWeight,
        glanceWeight,
        sleepyWeight,
        reactionWeight,
        workingWeight,
        swapSweep,
        swapWeight,
        dizzyWeight,
        dizzyTime,
        phoneHoldWeight,
        phoneTypingWeight,
        phoneMerongWeight,
        knockJoltWeight,
        annoyedTapWeight,
        extraHeadRoll,
        tiltDir: glanceTiltDir,
        headYaw: wantHeadYaw,
        headPitch: wantHeadPitch,
        headPitchCue: ctx.headPitchCue || 0,
        fingerOut: fingerScratch
      });

      const activeMode = overrideBeat !== "auto"
        ? overrideBeat
        : inDizzySway ? "dizzy"
          : (inDizzyShake || inDizzyRecover) ? "dizzy-recover"
            : inAnyKnock ? activeKnockKind
              : inPhoneAction ? ("phone-" + activePhoneStage)
                : swapStage !== "none" ? swapStage
                  : wantSleepy ? "sleepy"
                    : wantReaction ? "reaction"
                      : wantGlance ? "glance-up"
                        : wantWorking ? (wantTyping ? "working" : "working-read")
                          : (wantTyping ? "typing" : "reading");

      const updatedMetrics = props.getMetrics();
      return {
        beat: activeMode,
        mode: activeMode,
        activity,
        boneTargets,
        eyeYaw,
        eyePitch,
        exprOverlay,
        screenGlow: updatedMetrics.screenGlow,
        typing: typingWeight > 0.35,
        glanceUp: glanceWeight > 0.35,
        swapActive: inSwap,
        swapProgress,
        dizzyActive: inAnyDizzy,
        dizzyFxAlpha,
        dizzyTime,
        actionFxKind,
        actionFxAlpha,
        breakoutActive,
        breakoutMode,
        phonePose: wantPhoneHold ? "held" : (activePhoneStage === "buzz" ? "buzz" : (phoneIsFacedownNow ? "facedown" : "desk")),
        phoneFaceDown: phoneIsFacedownNow,
        hover: hovered
      };
    }

    function setLaptopAccent(hex) {
      const norm = normalizeHexColor(hex);
      if (!norm) return accentHex;
      accentHex = norm;
      targetAccentHex = norm;
      accentId = findPaletteEntryByHex(norm)?.id || "custom";
      props.applyAccentVisuals(accentHex, true);
      return accentHex;
    }

    function syncInitialSession(sessionId, meta) {
      const rid = typeof sessionId === "string" ? sessionId.trim() : "";
      if (!rid || activeSessionId) return getCoLeadState();
      activeSessionId = rid;
      activeSessionMeta = meta && typeof meta === "object" ? { ...meta } : null;
      const entry = sessionAccentForKey(rid);
      accentHex = entry.hex;
      targetAccentHex = entry.hex;
      accentId = entry.id;
      props.applyAccentVisuals(accentHex, true);
      return getCoLeadState();
    }

    function onSessionSwitch(sessionId, meta) {
      const rid = typeof sessionId === "string" ? sessionId.trim() : "";
      if (!rid) return { ...getCoLeadState(), swapped: false, dizzyTriggered: false };
      if (!activeSessionId) {
        syncInitialSession(rid, meta);
        return { ...getCoLeadState(), swapped: false, dizzyTriggered: false };
      }
      if (rid === activeSessionId) {
        if (meta && typeof meta === "object") {
          activeSessionMeta = { ...(activeSessionMeta || {}), ...meta };
        }
        return { ...getCoLeadState(), swapped: false, dizzyTriggered: false };
      }

      const nowSec = currentClockSec(meta?.nowMs);
      activeSessionId = rid;
      activeSessionMeta = meta && typeof meta === "object" ? { ...meta } : null;
      const entry = sessionAccentForKey(rid);
      targetAccentHex = entry.hex;
      accentId = entry.id;

      pruneSwitchWindow(nowSec);
      switchTimestamps.push(nowSec);

      if (switchTimestamps.length >= DIZZY_TRIGGER_COUNT && dizzyPhase === "none" && nowSec >= dizzyCooldownUntil) {
        switchTimestamps.length = 0;
        swapActive = false;
        swapElapsed = 0;
        swapOutDuration = SWAP_OUT_DURATION;
        dizzyPhase = "sway";
        dizzyElapsed = 0;
        accentHex = targetAccentHex;
        props.applyAccentVisuals(accentHex, false);
        return { ...getCoLeadState(), swapped: false, dizzyTriggered: true };
      }

      if (dizzyPhase !== "none") {
        accentHex = targetAccentHex;
        props.applyAccentVisuals(accentHex, false);
        return { ...getCoLeadState(), swapped: false, dizzyTriggered: false };
      }

      const pm = props.getMetrics();
      swapStartPose = { ...pm.liveLaptopOffset };
      const depthRatio = clamp(Math.abs(pm.liveLaptopOffset.dy) / Math.max(0.05, pm.baseVisH * 0.25), 0, 0.85);
      swapOutDuration = SWAP_OUT_DURATION * (1 - depthRatio * 0.45);
      swapDir = -swapDir;
      swapActive = true;
      swapElapsed = 0;
      swapSwitchedColor = false;
      return { ...getCoLeadState(), swapped: true, dizzyTriggered: false };
    }

    function setCoLeadBeat(beat) {
      if (beat === null || beat === undefined || beat === "") {
        overrideBeat = "auto";
        return overrideBeat;
      }
      const key = String(beat).trim().toLowerCase();
      if (!VALID_BEATS.has(key)) return overrideBeat;
      overrideBeat = key;
      if (key === "auto") {
        dizzyFxAlpha = dizzyPhase === "sway" ? 1 : 0;
      } else if (key === "glance-up") {
        glanceTiltDir = 1; glanceWeight = 1; typingWeight = 0; sleepyWeight = 0; workingWeight = 0; dizzyWeight = 0; dizzyFxAlpha = 0;
      } else if (key === "typing") {
        typingWeight = 1; glanceWeight = 0; sleepyWeight = 0; workingWeight = 0; dizzyWeight = 0; dizzyFxAlpha = 0;
      } else if (key === "working") {
        typingWeight = 1; workingWeight = 1; glanceWeight = 0; sleepyWeight = 0; dizzyWeight = 0; dizzyFxAlpha = 0;
      } else if (key === "sleepy") {
        sleepyWeight = 1; typingWeight = 0; glanceWeight = 0; workingWeight = 0; dizzyWeight = 0; dizzyFxAlpha = 0;
      } else if (key === "reading-left" || key === "reading-right") {
        typingWeight = 0.85; glanceWeight = 0; sleepyWeight = 0; dizzyWeight = 0; dizzyFxAlpha = 0;
      } else if (key === "swap-out" || key === "swap-in") {
        typingWeight = 0; glanceWeight = 0; sleepyWeight = 0; workingWeight = 0; dizzyWeight = 0; swapWeight = 1; dizzyFxAlpha = 0;
      } else if (key === "dizzy") {
        typingWeight = 0; glanceWeight = 0; sleepyWeight = 0; workingWeight = 0; swapWeight = 0; dizzyWeight = 1; dizzyFxAlpha = 1;
      } else if (key === "dizzy-shake" || key === "dizzy-recover") {
        typingWeight = 0; glanceWeight = 0; sleepyWeight = 0; workingWeight = 0; swapWeight = 0; dizzyWeight = 0;
        dizzyFxAlpha = key === "dizzy-shake" ? 0.30 : 0;
      } else if (key === "knock-startle" || key === "wakeup-jolt" || key === "annoyed-tap") {
        typingWeight = 0; glanceWeight = 0; sleepyWeight = 0; workingWeight = 0; swapWeight = 0; dizzyWeight = 0; dizzyFxAlpha = 0;
        phoneHoldWeight = 0; phoneTypingWeight = 0; phoneMerongWeight = 0;
        knockJoltWeight = key === "annoyed-tap" ? 0 : 1;
        annoyedTapWeight = key === "annoyed-tap" ? 1 : 0;
        actionFxKind = key;
        actionFxAlpha = 1;
      } else if (key === "phone-buzz" || key === "phone-pickup" || key === "phone-typing" || key === "phone-putdown" || key === "phone-facedown-merong") {
        typingWeight = 0; glanceWeight = 0; sleepyWeight = 0; workingWeight = 0; swapWeight = 0; dizzyWeight = 0; dizzyFxAlpha = 0;
        knockJoltWeight = 0; annoyedTapWeight = 0;
        phoneHoldWeight = (key === "phone-pickup" || key === "phone-typing") ? 1 : 0;
        phoneTypingWeight = key === "phone-typing" ? 1 : 0;
        phoneMerongWeight = key === "phone-facedown-merong" ? 1 : 0;
        phoneFaceDown = key === "phone-facedown-merong";
        actionFxKind = (key === "phone-buzz" || key === "phone-facedown-merong") ? key : "";
        actionFxAlpha = actionFxKind ? 1 : 0;
      }
      return overrideBeat;
    }

    function triggerKnock(opts = {}) {
      const nowSec = currentClockSec(opts?.nowMs);
      while (knockTimestamps.length && nowSec - knockTimestamps[0] > KNOCK_WINDOW_SEC) {
        knockTimestamps.shift();
      }
      const isSleepy = !!opts?.sleepy || sleepyWeight > 0.35 || overrideBeat === "sleepy" || lastActivity === "sleepy";
      if (overrideBeat === "sleepy") overrideBeat = "auto";
      knockTimestamps.push(nowSec);
      knockCount = knockTimestamps.length;

      if (isSleepy) {
        knockTimestamps.length = 0;
        knockCount = 1;
        sleepyWeight = 0;
        knockKind = "wakeup-jolt";
        knockElapsed = 0;
        knockDuration = KNOCK_WAKEUP_DURATION;
        return { kind: "wakeup_jolt", count: 1, wokeFromSleepy: true };
      }
      if (knockTimestamps.length >= KNOCK_ANNOYED_COUNT) {
        knockTimestamps.length = 0;
        knockKind = "annoyed-tap";
        knockElapsed = 0;
        knockDuration = KNOCK_ANNOYED_DURATION;
        return { kind: "annoyed_tap", count: knockCount, wokeFromSleepy: false };
      }
      knockKind = "knock-startle";
      knockElapsed = 0;
      knockDuration = KNOCK_STARTLE_DURATION;
      return { kind: "knock_startle", count: knockCount, wokeFromSleepy: false };
    }

    function triggerGesture(gestureName) {
      const g = typeof gestureName === "string" ? gestureName.trim().toLowerCase() : "";
      if (!g) return false;
      if (g === "phone_buzz") { phoneFaceDown = false; phoneMode = "buzz"; phoneAnimElapsed = 0; return true; }
      if (g === "phone_pickup") { phoneFaceDown = false; phoneMode = "pickup"; phoneAnimElapsed = 0; return true; }
      if (g === "phone_typing") { phoneFaceDown = false; phoneMode = "typing"; phoneAnimElapsed = 0; return true; }
      if (g === "phone_putdown") {
        // The server always emits putdown when it clears a hold, so a putdown
        // that finds the phone already resting is a no-op, not a replay.
        if (phoneMode === "desk" || phoneMode === "buzz" || phoneMode === "putdown") return true;
        phoneFaceDown = false; phoneMode = "putdown"; phoneAnimElapsed = 0; return true;
      }
      if (g === "phone_merong_facedown") { phoneFaceDown = true; phoneMode = "facedown-merong"; phoneAnimElapsed = 0; return true; }
      if (g === "knock_startle") { knockKind = "knock-startle"; knockElapsed = 0; knockDuration = KNOCK_STARTLE_DURATION; return true; }
      if (g === "wakeup_jolt") { knockKind = "wakeup-jolt"; knockElapsed = 0; knockDuration = KNOCK_WAKEUP_DURATION; return true; }
      if (g === "annoyed_tap") { knockKind = "annoyed-tap"; knockElapsed = 0; knockDuration = KNOCK_ANNOYED_DURATION; return true; }
      return false;
    }

    function setPhoneState(snap) {
      if (!snap || typeof snap !== "object") return getCoLeadState();
      const prevState = phoneLastSnapState;
      const nextState = typeof snap.state === "string" && snap.state ? snap.state : "laptop_ambient";
      const nextPosture = typeof snap.posture === "string" && snap.posture ? snap.posture : "laptop";
      const unread = Number.isFinite(snap.unreadAgent) ? Math.max(0, snap.unreadAgent) : 0;
      const faceDown = !!snap.phoneFaceDown;
      phoneUnreadAgent = unread;
      phoneFaceDown = faceDown;
      phoneLastSnapState = nextState;
      phoneSnapHeld = nextState === "phone_active" || nextPosture === "phone";
      phoneUnconfirmedElapsed = 0;

      if (phoneSnapHeld) {
        if (phoneMode !== "pickup" && phoneMode !== "typing") {
          phoneMode = "pickup";
          phoneAnimElapsed = 0;
        }
      } else if (unread > 0) {
        phoneMode = "buzz";
        phoneAnimElapsed = 0;
      } else if (prevState === "phone_active") {
        phoneMode = faceDown ? "facedown-merong" : "putdown";
        phoneAnimElapsed = 0;
      } else if (phoneMode === "pickup" || phoneMode === "typing" || phoneMode === "buzz") {
        phoneMode = faceDown ? "facedown-merong" : "putdown";
        phoneAnimElapsed = 0;
      }
      return getCoLeadState();
    }

    function setHover(flag) {
      explicitHover = flag === null || flag === undefined ? null : !!flag;
    }

    function setBreakoutState(mode) {
      const next = mode === "bleed" || mode === "roam" ? mode : "docked";
      breakoutMode = next;
      breakoutActive = next !== "docked";
      return breakoutActive;
    }

    function currentModeName() {
      if (overrideBeat !== "auto") return overrideBeat;
      if (dizzyPhase === "sway") return "dizzy";
      if (dizzyPhase === "recover") return "dizzy-recover";
      if (swapActive) {
        if (swapElapsed < swapOutDuration) return "swap-out";
        if (swapElapsed < swapOutDuration + SWAP_IN_DURATION) return "swap-in";
        return "swap-settle";
      }
      return phase;
    }

    function getCoLeadState() {
      const mode = currentModeName();
      const totalSwapDur = swapOutDuration + SWAP_IN_DURATION + SWAP_SETTLE_DURATION;
      const swapProg = overrideBeat === "swap-out" ? 0.22
        : overrideBeat === "swap-in" ? 0.56
          : swapActive ? clamp(swapElapsed / Math.max(0.05, totalSwapDur), 0, 1) : 0;
      const dizzyActiveNow = overrideBeat === "dizzy" || overrideBeat === "dizzy-shake" || overrideBeat === "dizzy-recover" || dizzyPhase !== "none";
      const dizzyProg = overrideBeat === "dizzy" ? 0.45
        : overrideBeat === "dizzy-shake" ? 0.84
          : overrideBeat === "dizzy-recover" ? 0.94
            : dizzyPhase === "sway" ? clamp(dizzyElapsed / DIZZY_TOTAL_DURATION, 0, 1)
              : dizzyPhase === "recover" ? clamp((DIZZY_SWAY_DURATION + dizzyElapsed) / DIZZY_TOTAL_DURATION, 0, 1) : 0;
      const pm = props.getMetrics();
      return {
        mode,
        beat: mode,
        overrideBeat,
        activity: lastActivity,
        activeSessionId,
        typing: typingWeight > 0.35,
        typingWeight: Number(typingWeight.toFixed(3)),
        glanceUp: glanceWeight > 0.35,
        glanceWeight: Number(glanceWeight.toFixed(3)),
        sleepyWeight: Number(sleepyWeight.toFixed(3)),
        workingWeight: Number(workingWeight.toFixed(3)),
        hover: explicitHover === true,
        laptopAccent: accentHex,
        targetLaptopAccent: targetAccentHex,
        laptopAccentId: accentId,
        swapActive: overrideBeat === "swap-out" || overrideBeat === "swap-in" || swapActive,
        swapProgress: Number(swapProg.toFixed(3)),
        liveLaptopOffsetY: Number(pm.liveLaptopOffset.dy.toFixed(4)),
        liveLaptopOffsetX: Number(pm.liveLaptopOffset.dx.toFixed(4)),
        dizzyActive: dizzyActiveNow,
        dizzyPhase: overrideBeat === "dizzy" ? "sway"
          : (overrideBeat === "dizzy-shake" || overrideBeat === "dizzy-recover") ? "recover"
            : dizzyPhase,
        dizzyProgress: Number(dizzyProg.toFixed(3)),
        dizzyFxAlpha: Number(dizzyFxAlpha.toFixed(3)),
        recentSwitchCount: switchTimestamps.length,
        screenGlow: Number(pm.screenGlow.toFixed(3)),
        hasLaptopProp: pm.hasLaptopProp,
        hasPhoneProp: pm.hasPhoneProp,
        phoneMode: overrideBeat.startsWith("phone-") ? overrideBeat : phoneMode,
        phonePose: phoneHoldWeight > 0.35 ? "held"
          : (overrideBeat === "phone-buzz" || phoneMode === "buzz") ? "buzz"
            : (overrideBeat === "phone-facedown-merong" || phoneFaceDown) ? "facedown" : "desk",
        phoneFaceDown: overrideBeat === "phone-facedown-merong" ? true : phoneFaceDown,
        phoneScreenGlow: Number(pm.phoneScreenGlow.toFixed(3)),
        knockActive: !!knockKind || overrideBeat === "knock-startle" || overrideBeat === "wakeup-jolt" || overrideBeat === "annoyed-tap",
        knockKind: (overrideBeat === "knock-startle" || overrideBeat === "wakeup-jolt" || overrideBeat === "annoyed-tap") ? overrideBeat : knockKind,
        knockCount,
        breakoutActive,
        breakoutMode,
        laptopTopViewportPct: Number(pm.lidTopViewportPct.toFixed(2)),
        chinViewportPct: Number(pm.chinViewportPct.toFixed(2)),
        headViewportPct: Number(pm.headViewportPct.toFixed(2)),
        palmGrip: { mode: pm.palmGrip.mode, side: pm.palmGrip.side, weight: Number(pm.palmGrip.weight.toFixed(3)), screenFacing: Number(pm.palmGrip.screenFacing.toFixed(3)), phoneUpDot: Number(pm.palmGrip.phoneUpDot.toFixed(3)) }
      };
    }

    return {
      layoutProp,
      step,
      syncPhoneToHand,
      setLaptopAccent,
      syncInitialSession,
      onSessionSwitch,
      setCoLeadBeat,
      setHover,
      setBreakoutState,
      triggerKnock,
      triggerGesture,
      setPhoneState,
      getPropObjects: props.getPropObjects,
      getCoLeadState,
      debugState: getCoLeadState,
      dispose: props.dispose
    };
  }

  Object.assign(C, {
    createVrmCoLead,
    sessionAccentForKey,
    vrmCoLeadMath: {
      SESSION_ACCENT_PALETTE,
      sessionAccentForKey,
      computeCoLeadTargets,
      classifyActivity,
      mirrorLR,
      normalizeHexColor,
      dampAlpha
    }
  });
})(window.WsCompanion);
