(function () {
  "use strict";

  const api = window.WsCompanionMessengerMockApi.createMockApi();

  // DOM References
  const root = document.documentElement;
  const rlistEl = document.querySelector("[data-rail-list]");
  const liveCountEl = document.querySelector("[data-live-count]");
  const unregCountEl = document.querySelector("[data-unreg-count]");
  const unregBodyEl = document.querySelector("[data-unreg-body]");

  const slotEl = document.getElementById("rail-companion-slot");
  const hitBtn = document.querySelector("[data-companion-hit]");
  const chatBtn = document.getElementById("companion-chat-btn");
  const bustWrapEl = document.querySelector("[data-companion-bust]");
  const modeLabelEl = document.querySelector("[data-stage-mode-label]");
  const unreadEl = document.querySelector("[data-stage-unread]");
  const quipEl = document.querySelector("[data-stage-quip]");
  const hintEl = document.querySelector("[data-stage-hint]");
  const knockLayerEl = document.querySelector("[data-knock-fx-layer]");

  const workspaceContentEl = document.querySelector("[data-workspace-content]");
  const domScanEl = document.querySelector("[data-dom-scan]");

  const popupEl = document.querySelector("[data-messenger-popup]");
  const caretEl = document.querySelector("[data-messenger-caret]");
  const avatarWrapEl = document.querySelector("[data-messenger-avatar]");
  const subtitleEl = document.querySelector("[data-messenger-subtitle]");
  const tier1Btn = popupEl.querySelector('[data-action="toggle-tier1"]');
  const pinBtn = popupEl.querySelector('[data-action="toggle-pin"]');
  const tier1StripEl = document.querySelector("[data-tier1-strip]");
  const tier1MetricsEl = document.querySelector("[data-tier1-metrics]");
  const tier1SummaryEl = document.querySelector("[data-tier1-summary]");
  const threadEl = document.querySelector("[data-messenger-thread]");
  const typingEl = document.querySelector("[data-messenger-typing]");
  const typingLabelEl = document.querySelector("[data-typing-label]");
  const inputEl = document.querySelector("[data-messenger-input]");
  const sendBtn = popupEl.querySelector('[data-action="send-message"]');
  const emojiPopEl = document.querySelector("[data-emoji-pop]");
  const emojiBtn = popupEl.querySelector('[data-action="toggle-emoji"]');
  const toastRegionEl = document.querySelector("[data-toast-region]");

  // Client UI state
  let workspaceSnapshot = null;
  let messengerState = null;
  let isBusy = false;
  let quipTimer = null;
  let swapTimer = null;
  let choreoSeq = 0;

  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

  function activeSession() {
    if (!workspaceSnapshot) return null;
    return (
      workspaceSnapshot.sessions.find((s) => s.id === workspaceSnapshot.activeSessionId) ||
      workspaceSnapshot.sessions[0]
    );
  }

  // ==========================================================================
  // Expressive SVG Character Generator (340x228 Bottom-Left Glass-Wall Stage)
  // Supports:
  //   typing | sleepy | startled | wake-jolt | phone-buzz | phone-check |
  //   smartphone-peek | merong | phone-facedown | laugh | annoyed-tap | dizzy
  // ==========================================================================
  function renderCompanionBustSVG(pose, accentHex, rimHex, phoneFaceDown) {
    const isLaugh = pose === "laugh";
    const isAnnoyed = pose === "annoyed-tap";
    const isDizzy = pose === "dizzy";
    const isSleepy = pose === "sleepy";
    const isStartled = pose === "startled";
    const isWakeJolt = pose === "wake-jolt";
    const isPhoneBuzz = pose === "phone-buzz";
    const isPhoneCheck = pose === "phone-check";
    const isPhonePeek = pose === "smartphone-peek";
    const isMerong = pose === "merong";
    const isPhoneFaceDown = pose === "phone-facedown";
    const isHoldingPhone = isPhoneCheck || isPhonePeek || isMerong || isLaugh || isAnnoyed;
    const deskShowsBack = isPhoneFaceDown || (!!phoneFaceDown && !isPhoneBuzz && !isHoldingPhone);

    const blushOpacity =
      isLaugh || isMerong
        ? "0.64"
        : isWakeJolt
        ? "0.60"
        : isAnnoyed || isStartled
        ? "0.50"
        : isSleepy
        ? "0.42"
        : "0.32";

    // Gaze direction: down at laptop, right toward buzzing/lifted phone, or forward when startled
    let gazeX = 0;
    let gazeY = 2.0;
    if (isPhoneBuzz || isPhoneFaceDown) {
      gazeX = 3.2;
      gazeY = 2.6;
    } else if (isPhoneCheck || isPhonePeek) {
      gazeX = 3.0;
      gazeY = 0.6;
    } else if (isStartled || isWakeJolt) {
      gazeX = 0;
      gazeY = -0.6;
    }

    // --- Eyes ---
    let eyesMarkup = "";
    if (isSleepy) {
      eyesMarkup = `
        <path d="M130 86 Q138 84 146 86" fill="none" stroke="#4a5375" stroke-width="1.7" stroke-linecap="round"/>
        <path d="M166 86 Q174 84 182 86" fill="none" stroke="#4a5375" stroke-width="1.7" stroke-linecap="round"/>
        <path d="M130 96 Q138 102 146 96" fill="none" stroke="#2c2e42" stroke-width="2.6" stroke-linecap="round"/>
        <path d="M166 96 Q174 102 182 96" fill="none" stroke="#2c2e42" stroke-width="2.6" stroke-linecap="round"/>
      `;
    } else if (isLaugh) {
      eyesMarkup = `
        <path d="M130 96 Q138 88 146 96" fill="none" stroke="#2c2e42" stroke-width="2.8" stroke-linecap="round"/>
        <path d="M166 96 Q174 88 182 96" fill="none" stroke="#2c2e42" stroke-width="2.8" stroke-linecap="round"/>
      `;
    } else if (isMerong) {
      // Adorable mischievous wink (left eye cheeky '>', right eye bright playful glint!)
      eyesMarkup = `
        <path d="M129 82 Q138 78 147 83" fill="none" stroke="#3c4362" stroke-width="2" stroke-linecap="round"/>
        <path d="M166 83 Q174 79 183 82" fill="none" stroke="#3c4362" stroke-width="2" stroke-linecap="round"/>
        <!-- Left eye playful wink '>' -->
        <path d="M131 91 L145 95.5 L131 100" fill="none" stroke="#25293e" stroke-width="2.7" stroke-linecap="round" stroke-linejoin="round"/>
        <!-- Right eye bright mischievous look -->
        <ellipse cx="174" cy="95" rx="7.6" ry="7.6" fill="#ffffff"/>
        <ellipse cx="175.8" cy="95.2" rx="5.3" ry="6.2" fill="url(#irisGrad)"/>
        <circle cx="175.8" cy="95.6" r="2.6" fill="#23294a"/>
        <circle cx="173.4" cy="92.4" r="2.1" fill="#ffffff"/>
        <circle cx="178" cy="98" r="1.1" fill="#d9ecff"/>
        <path d="M165.5 91 Q174 85.5 182.5 91" fill="none" stroke="#25293e" stroke-width="2.4" stroke-linecap="round"/>
      `;
    } else if (isDizzy) {
      eyesMarkup = `
        <g stroke="#3e425e" stroke-width="2.1" fill="none" stroke-linecap="round">
          <path d="M138 95 m-6 0 a6 6 0 1 1 11 2 a4 4 0 1 1 -7 -1 a2 2 0 1 1 3 0"/>
          <path d="M174 95 m-6 0 a6 6 0 1 1 11 2 a4 4 0 1 1 -7 -1 a2 2 0 1 1 3 0"/>
        </g>
      `;
    } else {
      const browMarkup = isAnnoyed
        ? `
          <path d="M128 82 L146 87" stroke="#3c4362" stroke-width="2.2" stroke-linecap="round"/>
          <path d="M184 82 L166 87" stroke="#3c4362" stroke-width="2.2" stroke-linecap="round"/>
        `
        : isStartled || isWakeJolt
        ? `
          <path d="M129 79 Q138 74 147 79" fill="none" stroke="#3f4769" stroke-width="2.1" stroke-linecap="round"/>
          <path d="M165 79 Q174 74 183 79" fill="none" stroke="#3f4769" stroke-width="2.1" stroke-linecap="round"/>
        `
        : isPhoneBuzz
        ? `
          <path d="M129 81 Q138 78 146 81" fill="none" stroke="#454d6e" stroke-width="1.9" stroke-linecap="round"/>
          <path d="M166 80 Q174 77 183 80" fill="none" stroke="#454d6e" stroke-width="1.9" stroke-linecap="round"/>
        `
        : `
          <path d="M129 83 Q138 80 146 83" fill="none" stroke="#4a5375" stroke-width="1.8" stroke-linecap="round"/>
          <path d="M166 83 Q174 80 183 83" fill="none" stroke="#4a5375" stroke-width="1.8" stroke-linecap="round"/>
        `;

      const eyeRy = isAnnoyed ? 6.0 : isWakeJolt ? 9.2 : isStartled ? 8.8 : 7.5;
      const pupilR = isStartled || isWakeJolt ? 2.1 : 2.6;
      eyesMarkup = `
        ${browMarkup}
        <g class="eyes-open">
          <!-- Left eye -->
          <ellipse cx="138" cy="95" rx="7.5" ry="${eyeRy}" fill="#ffffff"/>
          <ellipse cx="${138 + gazeX}" cy="${95.4 + gazeY}" rx="5.2" ry="${eyeRy - 1.3}" fill="url(#irisGrad)"/>
          <circle cx="${138 + gazeX}" cy="${95.8 + gazeY}" r="${pupilR}" fill="#23294a"/>
          <circle cx="${135.6 + gazeX * 0.5}" cy="${92.4 + gazeY * 0.5}" r="2.1" fill="#ffffff"/>
          <circle cx="${140.4 + gazeX * 0.5}" cy="${98.2 + gazeY * 0.4}" r="1.05" fill="#d9ecff"/>
          <path d="M129.5 91 Q138 85.5 146.5 91.5" fill="none" stroke="#25293e" stroke-width="2.4" stroke-linecap="round"/>
          <!-- Right eye -->
          <ellipse cx="174" cy="95" rx="7.5" ry="${eyeRy}" fill="#ffffff"/>
          <ellipse cx="${174 + gazeX}" cy="${95.4 + gazeY}" rx="5.2" ry="${eyeRy - 1.3}" fill="url(#irisGrad)"/>
          <circle cx="${174 + gazeX}" cy="${95.8 + gazeY}" r="${pupilR}" fill="#23294a"/>
          <circle cx="${171.6 + gazeX * 0.5}" cy="${92.4 + gazeY * 0.5}" r="2.1" fill="#ffffff"/>
          <circle cx="${176.4 + gazeX * 0.5}" cy="${98.2 + gazeY * 0.4}" r="1.05" fill="#d9ecff"/>
          <path d="M165.5 91.5 Q174 85.5 182.5 91" fill="none" stroke="#25293e" stroke-width="2.4" stroke-linecap="round"/>
        </g>
      `;
    }

    // --- Mouth ---
    let mouthMarkup = "";
    if (isMerong) {
      // Adorable cheeky grin + pink tongue sticking out ("메-롱!")
      mouthMarkup = `
        <path d="M148 110 Q156 114.5 164 110" fill="none" stroke="#8a4452" stroke-width="2.1" stroke-linecap="round"/>
        <path d="M151.5 111.5 C151.5 121.5 160.5 121.5 160.5 111.5 Z" fill="#f2728b" stroke="#964255" stroke-width="1.25" stroke-linejoin="round"/>
        <path d="M156 112.5 L156 117.5" stroke="#c84c65" stroke-width="1.1" stroke-linecap="round" opacity="0.72"/>
      `;
    } else if (isLaugh) {
      mouthMarkup = `<path d="M149 110 Q156 119 163 110 Z" fill="#e67e8d" stroke="#8a4452" stroke-width="1.3" stroke-linejoin="round"/>`;
    } else if (isWakeJolt) {
      mouthMarkup = `<ellipse cx="156" cy="112" rx="4.4" ry="5.0" fill="#e87d8d" stroke="#8c4c57" stroke-width="1.35"/>`;
    } else if (isStartled) {
      mouthMarkup = `<ellipse cx="156" cy="112" rx="3.4" ry="4.1" fill="#e88a96" stroke="#8c4c57" stroke-width="1.25"/>`;
    } else if (isSleepy) {
      mouthMarkup = `<ellipse cx="156" cy="112" rx="2.4" ry="1.8" fill="#d98c96" opacity="0.8"/>`;
    } else if (isAnnoyed) {
      mouthMarkup = `<path d="M151 112 Q156 108.5 161 112" fill="none" stroke="#8c4c57" stroke-width="2" stroke-linecap="round"/>`;
    } else if (isDizzy) {
      mouthMarkup = `<path d="M150 112 Q153 109 156 112 Q159 115 162 112" fill="none" stroke="#8c4c57" stroke-width="1.8" stroke-linecap="round"/>`;
    } else if (isPhoneFaceDown) {
      mouthMarkup = `<path d="M151 110.5 Q157 114.2 162 109.5" fill="none" stroke="#965662" stroke-width="1.85" stroke-linecap="round"/>`;
    } else {
      mouthMarkup = `<path d="M150.5 110.5 Q156 115.2 161.5 110.5" fill="none" stroke="#965662" stroke-width="1.8" stroke-linecap="round"/>`;
    }

    // --- Desk Smartphone (Resting Face-Up / Buzzing / Flipped Face-Down) ---
    const deskPhoneInner = deskShowsBack
      ? `
        <!-- Flipped FACE-DOWN: pastel metallic back + camera bump facing UP -->
        <rect x="-25" y="-11" width="50" height="22" rx="6" fill="#232734"/>
        <rect x="-23" y="-9.5" width="46" height="19" rx="4.8" fill="url(#phoneGrad)" opacity="0.95"/>
        <!-- Camera island facing up -->
        <rect x="-18" y="-6.5" width="14" height="9" rx="3" fill="#181b25" opacity="0.92"/>
        <circle cx="-14" cy="-2" r="2.0" fill="#5e6b92"/>
        <circle cx="-8.2" cy="-2" r="2.0" fill="#5e6b92"/>
        <circle cx="15" cy="0" r="2.2" fill="#ffffff" opacity="0.65"/>
        ${
          isPhoneFaceDown
            ? `
          <!-- Crisp '탁!' flip-down impact cue -->
          <g stroke="#f8b4cb" stroke-width="1.9" fill="none" stroke-linecap="round">
            <path d="M-29 -14 L-34 -20"/>
            <path d="M29 -14 L34 -20"/>
            <path d="M0 -17 L0 -24"/>
          </g>
          <text x="0" y="-26" text-anchor="middle" fill="#f8b4cb" font-size="10.5" font-weight="800" font-family="system-ui, sans-serif">탁!</text>
        `
            : ""
        }
      `
      : `
        <!-- Resting FACE-UP beside laptop (lights up bright on phone-buzz!) -->
        <rect x="-25" y="-11" width="50" height="22" rx="6" fill="#1b1e28"/>
        <rect x="-22.5" y="-8.8" width="45" height="17.6" rx="4.2" fill="${isPhoneBuzz ? "url(#phoneGlowGrad)" : "#282d3d"}"/>
        ${
          isPhoneBuzz
            ? `
          <rect x="-17" y="-5" width="34" height="5.5" rx="2.7" fill="#ffffff" opacity="0.92"/>
          <rect x="-12" y="2.5" width="24" height="3.2" rx="1.6" fill="#1b2338" opacity="0.45"/>
        `
            : `
          <rect x="-10" y="4.5" width="20" height="2" rx="1" fill="#7c87a8" opacity="0.55"/>
          <circle cx="-15" cy="-3" r="1.6" fill="${accentHex}" opacity="0.65"/>
        `
        }
      `;

    return `
      <svg class="companion-svg" viewBox="0 0 340 228" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
        <defs>
          <radialGradient id="screenBounce" cx="46%" cy="85%" r="60%">
            <stop offset="0%" stop-color="${accentHex}" stop-opacity="0.34"/>
            <stop offset="70%" stop-color="${accentHex}" stop-opacity="0.08"/>
            <stop offset="100%" stop-color="${accentHex}" stop-opacity="0"/>
          </radialGradient>
          <linearGradient id="hairGrad" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stop-color="#3c4668"/>
            <stop offset="55%" stop-color="#2e3552"/>
            <stop offset="100%" stop-color="#22273e"/>
          </linearGradient>
          <linearGradient id="irisGrad" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stop-color="#3d4d8c"/>
            <stop offset="65%" stop-color="${accentHex}"/>
            <stop offset="100%" stop-color="${rimHex}"/>
          </linearGradient>
          <linearGradient id="hoodieGrad" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stop-color="#363c4c"/>
            <stop offset="100%" stop-color="#252934"/>
          </linearGradient>
          <linearGradient id="laptopLidGrad" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stop-color="#323642"/>
            <stop offset="100%" stop-color="#1e212a"/>
          </linearGradient>
          <linearGradient id="phoneGrad" x1="0" y1="0" x2="1" y2="1">
            <stop offset="0%" stop-color="${rimHex}"/>
            <stop offset="100%" stop-color="${accentHex}"/>
          </linearGradient>
          <linearGradient id="phoneGlowGrad" x1="0" y1="0" x2="1" y2="1">
            <stop offset="0%" stop-color="#fef08a"/>
            <stop offset="55%" stop-color="${rimHex}"/>
            <stop offset="100%" stop-color="${accentHex}"/>
          </linearGradient>
        </defs>

        <!-- Ambient screen bounce behind & around character -->
        <ellipse cx="156" cy="176" rx="134" ry="82" fill="url(#screenBounce)"/>

        <g class="char-body">
          <!-- Back flowing hair -->
          <path d="M100 85 C92 126 88 164 98 190 C120 200 192 200 214 190 C224 164 220 126 212 85 C206 46 182 32 156 32 C130 32 106 46 100 85 Z" fill="url(#hairGrad)"/>

          <!-- Shoulders & Cozy Oversized Hoodie -->
          <path d="M84 224 C90 170 112 148 138 143 L174 143 C200 148 222 170 228 224 Z" fill="url(#hoodieGrad)"/>
          <!-- Inner cream collar & ribbon -->
          <path d="M139 142 L156 164 L173 142 Z" fill="#efe9df"/>
          <path d="M147 152 L156 162 L165 152" fill="none" stroke="${accentHex}" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"/>
          <circle cx="156" cy="161" r="3.4" fill="${accentHex}"/>

          <!-- Neck -->
          <rect x="146" y="122" width="20" height="25" rx="9" fill="#f6d8c5"/>
          <path d="M146 128 Q156 135 166 128 L166 135 Q156 141 146 135 Z" fill="#e8bfa8" opacity="0.55"/>

          <!-- Head Group -->
          <g class="char-head">
            <!-- Ears -->
            <ellipse cx="114" cy="97" rx="6" ry="8" fill="#f5d5c1"/>
            <ellipse cx="198" cy="97" rx="6" ry="8" fill="#f5d5c1"/>

            <!-- Face silhouette -->
            <path d="M116 81 C116 51 134 41 156 41 C178 41 196 51 196 81 C196 109 180 129 156 131 C132 129 116 109 116 81 Z" fill="#fde3d2"/>

            <!-- Warm blush -->
            <ellipse cx="130" cy="105" rx="9.5" ry="5" fill="#f48b9c" opacity="${blushOpacity}"/>
            <ellipse cx="182" cy="105" rx="9.5" ry="5" fill="#f48b9c" opacity="${blushOpacity}"/>

            <!-- Subtle nose dot -->
            <circle cx="156" cy="102.5" r="1.1" fill="#d8a58e"/>

            <!-- Expressive Eyes & Mouth -->
            ${eyesMarkup}
            ${mouthMarkup}

            <!-- Front Bangs & Side Locks -->
            <path d="M112 84 C110 51 130 34 156 34 C182 34 202 51 200 84 C194 71 186 61 180 59 C182 70 179 79 173 84 C172 72 167 63 162 59 C161 71 156 80 150 84 C149 72 145 62 140 59 C138 69 134 78 128 83 C125 72 121 65 117 63 Z" fill="url(#hairGrad)"/>
            <path d="M113 75 C106 105 108 135 116 155 C120 139 120 115 120 91 Z" fill="url(#hairGrad)"/>
            <path d="M199 75 C206 105 204 135 196 155 C192 139 192 115 192 91 Z" fill="url(#hairGrad)"/>

            <!-- Hair specular shine arc -->
            <path d="M130 54 Q156 46 182 54" fill="none" stroke="#99b1ec" stroke-width="2.5" stroke-linecap="round" opacity="0.45"/>

            <!-- Cute hairpin matching session accent -->
            <g transform="translate(188, 67) rotate(18)">
              <rect x="-8" y="-2.5" width="16" height="5" rx="2.5" fill="${accentHex}"/>
              <circle cx="5" cy="0" r="2.2" fill="#fff"/>
            </g>

            <!-- Cute ahoge curl -->
            <path d="M156 35 C159 22 170 19 172 27 C166 26 161 30 159 36" fill="none" stroke="#3c4668" stroke-width="2.6" stroke-linecap="round"/>

            <!-- Annoyed cute spark mark -->
            ${isAnnoyed ? `
              <g transform="translate(200, 52)" stroke="#f6ad7b" stroke-width="2.2" fill="none" stroke-linecap="round">
                <path d="M-5 -1 Q-1 -1 -1 -5"/>
                <path d="M5 -1 Q1 -1 1 -5"/>
                <path d="M-5 5 Q-1 5 -1 9"/>
                <path d="M5 5 Q1 5 1 9"/>
              </g>
            ` : ""}

            <!-- Wake-up jolt flustered sweat drop -->
            ${isWakeJolt ? `
              <path d="M192 78 C195 85 196 90 192 92 C188 90 189 85 192 78 Z" fill="#7dd3fc" stroke="#38bdf8" stroke-width="1"/>
            ` : ""}
          </g>

          <!-- Sleepy zZz FX -->
          <g class="sleepy-fx" fill="#c4b5fd" font-family="system-ui, sans-serif" font-weight="800">
            <text class="zzz-1" x="206" y="76" font-size="13">z</text>
            <text class="zzz-2" x="218" y="60" font-size="16">Z</text>
            <text class="zzz-3" x="234" y="46" font-size="19">z</text>
          </g>

          <!-- Startled & Wake-up Jolt burst marks -->
          <g class="startle-fx" stroke="#fbbf24" stroke-width="2.6" fill="none" stroke-linecap="round">
            <path d="M98 56 L108 66"/>
            <path d="M92 74 L104 78"/>
            <path d="M214 54 L204 64"/>
            <path d="M222 72 L210 76"/>
            <text x="212" y="48" fill="#fbbf24" stroke="none" font-size="16" font-weight="900" font-family="system-ui, sans-serif">${isWakeJolt ? "!!" : "!"}</text>
          </g>

          <!-- Cheeky Merong sparkles & badge -->
          <g class="merong-fx">
            <path d="M92 64 L94.5 57 L97 64 L104 66.5 L97 69 L94.5 76 L92 69 L85 66.5 Z" fill="#fbcfe8"/>
            <path d="M212 52 L214 46 L216 52 L222 54 L216 56 L214 62 L212 56 L206 54 Z" fill="#fde68a"/>
            <rect x="64" y="80" width="44" height="20" rx="10" fill="#f472b6" opacity="0.92"/>
            <text x="86" y="94" text-anchor="middle" fill="#ffffff" font-size="10.5" font-weight="800" font-family="system-ui, sans-serif">메-롱</text>
          </g>

          <!-- Laughing floating sparkles -->
          <g class="laugh-fx">
            <path d="M90 60 L93 52 L96 60 L104 63 L96 66 L93 74 L90 66 L82 63 Z" fill="#fde68a"/>
            <path d="M216 50 L218 44 L220 50 L226 52 L220 54 L218 60 L216 54 L210 52 Z" fill="#fbcfe8"/>
            <circle cx="210" cy="76" r="3" fill="${rimHex}"/>
          </g>

          <!-- Dizzy FX -->
          <g class="dizzy-fx">
            <ellipse cx="156" cy="30" rx="52" ry="10" fill="none" stroke="#fde68a" stroke-width="1.6" stroke-dasharray="18 12" opacity="0.72"/>
            <g class="dizzy-stars">
              <path d="M114 30 L116.5 24 L119 30 L125 32.5 L119 35 L116.5 41 L114 35 L108 32.5 Z" fill="#fde68a"/>
              <path d="M196 25 L198 20 L200 25 L205 27 L200 29 L198 34 L196 29 L191 27 Z" fill="#7dd3fc"/>
              <path d="M156 17 L158 12 L160 17 L165 19 L160 21 L158 26 L156 21 L151 19 Z" fill="#fbcfe8"/>
            </g>
            <g class="dizzy-spiral-r" stroke="#fde68a" stroke-width="2.2" fill="none" stroke-linecap="round">
              <path d="M230 66 m0 0 c2-2 6 0 5 4 c-1 5-8 6-11 2 c-4-5-2-13 5-15 c7-2 15 3 16 11"/>
            </g>
            <g class="dizzy-spiral-l" stroke="#fbcfe8" stroke-width="1.9" fill="none" stroke-linecap="round">
              <path d="M84 72 m0 0 c-2-2-5 0-4 3 c1 4 7 5 9 1 c3-4 1-10-4-12 c-6-2-12 2-13 9"/>
            </g>
          </g>

          <!-- Prop A: Sleek Beveled Foreground Laptop Lid (slightly left-centered so smartphone rests cleanly on right!) -->
          <g class="prop-laptop">
            <g class="laptop-hands" fill="#fde3d2">
              <ellipse cx="116" cy="171" rx="10.5" ry="7.5"/>
              <ellipse cx="186" cy="171" rx="10.5" ry="7.5"/>
            </g>
            <rect x="70" y="168" width="154" height="62" rx="10" fill="${accentHex}" opacity="0.22"/>
            <rect x="73" y="171" width="148" height="60" rx="9" fill="url(#laptopLidGrad)"/>
            <rect x="82" y="172.5" width="130" height="2" rx="1" fill="${rimHex}" opacity="0.65"/>
            <g transform="translate(147, 198)">
              <circle cx="0" cy="0" r="9.5" fill="${accentHex}" opacity="0.22"/>
              <path d="M-4.6 -2 L-2 3.8 L0 -1 L2 3.8 L4.6 -2" fill="none" stroke="${rimHex}" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"/>
            </g>
          </g>

          <!-- Prop B1: Smartphone Resting on Desk Beside Laptop (Face-Up / Buzzing / Flipped Face-Down) -->
          <g class="prop-phone-desk">
            <!-- Soft desk shadow -->
            <ellipse cx="274" cy="206" rx="28" ry="6" fill="#000000" opacity="0.28"/>
            <!-- Buzzing notification waves & glow (Beat 1: phone-buzz) -->
            <g class="phone-buzz-waves">
              <ellipse cx="274" cy="194" rx="40" ry="24" fill="${accentHex}" opacity="0.22"/>
              <path d="M244 176 Q238 184 244 192" fill="none" stroke="#fde68a" stroke-width="2.2" stroke-linecap="round"/>
              <path d="M304 176 Q310 184 304 192" fill="none" stroke="#fde68a" stroke-width="2.2" stroke-linecap="round"/>
              <path d="M258 164 Q274 156 290 164" fill="none" stroke="${rimHex}" stroke-width="2" stroke-linecap="round"/>
              <rect x="252" y="138" width="44" height="20" rx="10" fill="#fde68a"/>
              <text x="274" y="152" text-anchor="middle" fill="#1b1d21" font-size="10.5" font-weight="800" font-family="system-ui, sans-serif">윙- ♪</text>
            </g>
            <g transform="translate(274, 196) rotate(-7)">
              <g class="desk-phone-body">
                ${deskPhoneInner}
              </g>
            </g>
          </g>

          <!-- Prop B2: One-Hand Lifted Smartphone (Beat 2 & 3: lifted cleanly with ONE hand beside laptop!) -->
          <g class="prop-phone-hand">
            <!-- Soft screen glow toward her face -->
            <ellipse cx="242" cy="144" rx="44" ry="38" fill="${accentHex}" opacity="0.22"/>
            <!-- Right forearm / wrist lifting the phone -->
            <path d="M236 226 C244 202 252 184 260 168 L276 175 C268 194 264 210 258 226 Z" fill="#2e3342"/>
            <!-- One hand wrapping the smartphone (right palm + curled fingers on back/edge) -->
            <ellipse cx="268" cy="158" rx="13" ry="14" fill="#f7d5bf" transform="rotate(14 268 158)"/>
            <!-- Sleek pastel smartphone held vertically in one hand -->
            <g transform="translate(254, 146) rotate(10)">
              <rect x="-21" y="-34" width="42" height="68" rx="9" fill="#202430"/>
              <rect x="-19" y="-32" width="38" height="64" rx="7.5" fill="url(#phoneGrad)" opacity="0.95"/>
              <!-- Camera module -->
              <rect x="-13" y="-26" width="12" height="16" rx="4" fill="#181b25" opacity="0.9"/>
              <circle cx="-7" cy="-21" r="2.3" fill="#4f5978"/>
              <circle cx="-7" cy="-14.5" r="2.3" fill="#4f5978"/>
              <!-- Mini chat bubble emblem on case -->
              <rect x="-5" y="4" width="17" height="8" rx="4" fill="#ffffff" opacity="0.9"/>
              <rect x="-11" y="15" width="14" height="7" rx="3.5" fill="#1e2230" opacity="0.32"/>
            </g>
            <!-- Single thumb gripping the left bezel (one-hand hold!) -->
            <ellipse cx="236" cy="152" rx="7.2" ry="4.8" fill="#fde3d2" transform="rotate(-18 236 152)"/>
          </g>

          <!-- Prop C: Playful tapping hand on the glass (Annoyed-Tap pose) -->
          <g class="prop-tap-hand">
            <circle cx="102" cy="134" r="17" fill="none" stroke="${rimHex}" stroke-width="1.6" opacity="0.55"/>
            <circle cx="102" cy="134" r="9.5" fill="none" stroke="#f6ad7b" stroke-width="1.8" opacity="0.75"/>
            <path d="M90 156 C86 144 89 130 98 128 C103 127 107 131 108 137 L111 152 C112 160 104 166 97 164 C92 163 91 159 90 156 Z" fill="#fde3d2"/>
          </g>
        </g>
      </svg>
    `;
  }

  function renderMiniAvatarSVG(emotion, accentHex) {
    const isLaugh = emotion === "laugh";
    const isAnnoyed = emotion === "annoyed";
    const isDizzy = emotion === "dizzy";
    const isSleepy = emotion === "sleepy";
    const isStartled = emotion === "startled";
    const isMerong = emotion === "merong";

    let eyes = "";
    if (isLaugh) {
      eyes = `<path d="M13.5 19 Q15.5 16.5 17.5 19" fill="none" stroke="#25293e" stroke-width="1.6" stroke-linecap="round"/><path d="M22.5 19 Q24.5 16.5 26.5 19" fill="none" stroke="#25293e" stroke-width="1.6" stroke-linecap="round"/>`;
    } else if (isSleepy) {
      eyes = `<path d="M13.5 19 Q15.5 21.2 17.5 19" fill="none" stroke="#25293e" stroke-width="1.6" stroke-linecap="round"/><path d="M22.5 19 Q24.5 21.2 26.5 19" fill="none" stroke="#25293e" stroke-width="1.6" stroke-linecap="round"/>`;
    } else if (isMerong) {
      eyes = `<path d="M13.5 17.5 L17 19 L13.5 20.5" fill="none" stroke="#25293e" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/><circle cx="24.5" cy="19" r="2.1" fill="#293154"/><circle cx="23.9" cy="18.3" r=".75" fill="#fff"/>`;
    } else if (isDizzy) {
      eyes = `<circle cx="15.5" cy="19" r="2.2" fill="none" stroke="#25293e" stroke-width="1.4"/><circle cx="24.5" cy="19" r="2.2" fill="none" stroke="#25293e" stroke-width="1.4"/>`;
    } else if (isStartled) {
      eyes = `<circle cx="15.5" cy="19" r="2.6" fill="#fff"/><circle cx="15.5" cy="19" r="1.4" fill="#293154"/><circle cx="24.5" cy="19" r="2.6" fill="#fff"/><circle cx="24.5" cy="19" r="1.4" fill="#293154"/>`;
    } else {
      eyes = `<circle cx="15.5" cy="19" r="2.1" fill="#293154"/><circle cx="24.5" cy="19" r="2.1" fill="#293154"/><circle cx="14.9" cy="18.3" r=".75" fill="#fff"/><circle cx="23.9" cy="18.3" r=".75" fill="#fff"/>`;
    }

    let mouth = "";
    if (isMerong) {
      mouth = `<path d="M17.5 23.5 Q20 25.2 22.5 23.5" fill="none" stroke="#8a4452" stroke-width="1.3" stroke-linecap="round"/><path d="M18.6 24.1 C18.6 27.6 21.4 27.6 21.4 24.1 Z" fill="#f2728b"/>`;
    } else if (isLaugh) {
      mouth = `<path d="M17.5 23.5 Q20 26.8 22.5 23.5 Z" fill="#e67e8d"/>`;
    } else if (isStartled) {
      mouth = `<circle cx="20" cy="24.5" r="1.6" fill="#e88a96"/>`;
    } else if (isAnnoyed) {
      mouth = `<path d="M18 24.5 Q20 23 22 24.5" fill="none" stroke="#8c4c57" stroke-width="1.4" stroke-linecap="round"/>`;
    } else {
      mouth = `<path d="M18 23.8 Q20 25.6 22 23.8" fill="none" stroke="#965662" stroke-width="1.35" stroke-linecap="round"/>`;
    }

    return `
      <svg viewBox="0 0 40 40" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
        <path d="M7 22 C7 10 13 6 20 6 C27 6 33 10 33 22 C33 30 31 35 28 38 L12 38 C9 35 7 30 7 22 Z" fill="#2d3450"/>
        <path d="M10 20 C10 12 14 9 20 9 C26 9 30 12 30 20 C30 27 25.5 31 20 31.5 C14.5 31 10 27 10 20 Z" fill="#fde3d2"/>
        <ellipse cx="13.5" cy="22" rx="2.6" ry="1.4" fill="#f48b9c" opacity="0.45"/>
        <ellipse cx="26.5" cy="22" rx="2.6" ry="1.4" fill="#f48b9c" opacity="0.45"/>
        ${eyes}
        ${mouth}
        <path d="M9.5 18 C10 10 14 7 20 7 C26 7 30 10 30.5 18 C28 14 25 12 22 14 C20 11 17 12 15 15 C13 13 11 14 9.5 18 Z" fill="#353e5f"/>
        <rect x="25.5" y="12.5" width="5.5" height="2" rx="1" fill="${accentHex}" transform="rotate(18 28 13.5)"/>
      </svg>
    `;
  }

  // ==========================================================================
  // Positioning: Anchor floating messenger popup cleanly beside bottom-left #rail-companion-slot
  // ==========================================================================
  function positionMessengerPopup() {
    if (!slotEl || !popupEl) return;
    const slotRect = slotEl.getBoundingClientRect();
    const popW = Math.min(368, window.innerWidth - 24);
    const popH = Math.min(552, window.innerHeight - 68);

    const desiredLeft = slotRect.right + 10;
    const left = Math.max(12, Math.min(desiredLeft, window.innerWidth - popW - 12));

    // Align cleanly flush with the bottom of the bottom-left companion territory
    const desiredBottom = Math.max(12, window.innerHeight - slotRect.bottom);
    const maxBottom = Math.max(12, window.innerHeight - popH - 58);
    const bottom = Math.min(desiredBottom, maxBottom);

    popupEl.style.left = `${Math.round(left)}px`;
    popupEl.style.bottom = `${Math.round(bottom)}px`;
    popupEl.style.height = `${Math.round(popH)}px`;

    // Point the subtle left caret at the companion stage
    const popupBottomY = window.innerHeight - bottom;
    const slotMidY = slotRect.top + slotRect.height * 0.48;
    const caretFromBottom = Math.max(26, Math.min(popH - 56, popupBottomY - slotMidY));
    caretEl.style.bottom = `${Math.round(caretFromBottom)}px`;
  }

  // ==========================================================================
  // Rendering Helpers
  // ==========================================================================
  function escapeHtml(str) {
    return String(str)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function showToast(text) {
    const el = document.createElement("div");
    el.className = "toast";
    el.textContent = text;
    toastRegionEl.appendChild(el);
    setTimeout(() => {
      el.remove();
    }, 2600);
  }

  function applySessionAccent(accent) {
    if (!accent) return;
    root.style.setProperty("--session-accent", accent.hex);
    root.style.setProperty("--session-rim", accent.rim);
  }

  function renderRail() {
    if (!workspaceSnapshot) return;
    const { workspace, sessions, activeSessionId, unregistered } = workspaceSnapshot;
    const active = activeSession();
    applySessionAccent(active.accent);

    liveCountEl.textContent = `${sessions.length} live`;
    unregCountEl.textContent = String(unregistered.length);

    rlistEl.innerHTML = `
      <div class="rws">
        <div class="wsbar">
          <svg class="i" aria-hidden="true"><use href="#i-folder"/></svg>
          <span class="nm">${escapeHtml(workspace.name)}</span>
          <span class="pp">${escapeHtml(workspace.path)}</span>
          <span class="ct">${sessions.length}</span>
        </div>
        <div class="wthd">
          <svg class="i" aria-hidden="true"><use href="#i-branch"/></svg>
          <span class="br">${escapeHtml(workspace.branch)}</span>
          <span class="gs">${escapeHtml(workspace.gitClean)}</span>
        </div>
        <div class="rsessions">
          ${sessions
            .map((s) => {
              const isCur = s.id === activeSessionId;
              return `
                <button
                  type="button"
                  class="rs${isCur ? " cur" : ""}"
                  data-session-id="${escapeHtml(s.id)}"
                  style="--row-accent: ${escapeHtml(s.accent.hex)}"
                  aria-current="${isCur ? "true" : "false"}"
                >
                  <span class="rs-top">
                    <span class="rs-dot${s.status === "working" ? " working" : ""}" aria-hidden="true"></span>
                    <span class="rs-title">${escapeHtml(s.title)}</span>
                    <span class="rs-model${s.upgraded ? " upgraded" : ""}">${escapeHtml(s.model)}</span>
                  </span>
                  <span class="rs-sub">
                    <span class="rs-msg">${escapeHtml(s.lastMsg)}</span>
                    <span class="rs-time">${escapeHtml(s.updatedAt)}</span>
                  </span>
                </button>
              `;
            })
            .join("")}
        </div>
      </div>
    `;

    unregBodyEl.innerHTML = unregistered
      .map(
        (u) => `
        <button type="button" class="unreg-item" data-unreg-name="${escapeHtml(u.name)}">
          <div>
            <strong>${escapeHtml(u.name)}</strong>
            <div><code>${escapeHtml(u.path)}</code></div>
          </div>
          <span class="unreg-count">${u.count}</span>
        </button>
      `
      )
      .join("");
  }

  function renderStage() {
    if (!workspaceSnapshot || !messengerState) return;
    const active = activeSession();

    slotEl.dataset.pose = messengerState.pose;
    slotEl.dataset.phoneState = messengerState.phoneFaceDown ? "facedown" : "faceup";
    modeLabelEl.textContent = messengerState.stageCaption;

    // Top-right speech-bubble button owns open/close semantics
    chatBtn.setAttribute("aria-expanded", messengerState.open ? "true" : "false");
    chatBtn.setAttribute("aria-pressed", messengerState.open ? "true" : "false");
    chatBtn.setAttribute(
      "aria-label",
      messengerState.open ? "컴패니언 메신저 닫기" : "컴패니언 메신저 열기"
    );

    hitBtn.setAttribute(
      "aria-label",
      messengerState.pose === "sleepy"
        ? "유리창을 톡톡 두드려 졸고 있는 컴패니언 깨우기"
        : "유리창을 톡톡 두드려 컴패니언 반응 보기 (메신저 열지 않음)"
    );

    bustWrapEl.innerHTML = renderCompanionBustSVG(
      messengerState.pose,
      active.accent.hex,
      active.accent.rim,
      messengerState.phoneFaceDown
    );

    if (messengerState.unreadCount > 0 && !messengerState.open) {
      unreadEl.hidden = false;
      unreadEl.textContent = String(messengerState.unreadCount);
    } else {
      unreadEl.hidden = true;
    }

    if (messengerState.stageQuip) {
      quipEl.hidden = false;
      quipEl.textContent = messengerState.stageQuip;
      clearTimeout(quipTimer);
      if (messengerState.pose !== "sleepy" && messengerState.pose !== "dizzy") {
        quipTimer = setTimeout(() => {
          if (messengerState.pose !== "sleepy" && messengerState.pose !== "dizzy") {
            quipEl.hidden = true;
          }
        }, 3800);
      }
    } else {
      quipEl.hidden = true;
    }

    if (messengerState.pose === "sleepy") {
      hintEl.textContent = "유리 톡톡 = 화들짝 깨우기 · 우상단 💬 = 메신저";
    } else if (messengerState.open) {
      hintEl.textContent = "메시지 전송 = 스마트폰 알림 · 유리 톡톡 = 깜짝 반응";
    } else {
      hintEl.textContent = "유리 톡톡 = 깜짝 반응 · 우상단 💬 = 메신저";
    }

    // Sync messenger header mini-avatar
    avatarWrapEl.innerHTML = `
      ${renderMiniAvatarSVG(messengerState.emotion, active.accent.hex)}
      <span class="cm-avatar-status" aria-hidden="true"></span>
    `;
  }

  function renderWorkspace() {
    if (!workspaceSnapshot) return;
    const { sessions, currentView, globalSettings } = workspaceSnapshot;
    const active = activeSession();

    if (currentView === "companion-settings") {
      workspaceContentEl.innerHTML = `
        <div class="ws-topbar">
          <div class="ws-title-group">
            <svg class="i" aria-hidden="true"><use href="#i-gear"/></svg>
            <h1>워크플로 설정 · 컴패니언</h1>
            <span class="ws-route-chip">/settings?section=companion</span>
          </div>
          <button type="button" class="ws-back-btn" data-action="back-to-session">← 세션 보드로 돌아가기</button>
        </div>
        <div class="ws-settings-card">
          <div class="ws-panel-head">
            <h2>전역 컴패니언 구성 (~/.ws/config.json)</h2>
            <span class="ws-badge ok">Tier-2 ws/daemon.ui 이동 연동</span>
          </div>
          <div class="ws-setting-row">
            <div class="ws-setting-info">
              <strong>컴패니언 활성화</strong>
              <span>좌하단 전용 영역 고정 (Unregistered workspaces 토글 시 0px 흔들림)</span>
            </div>
            <span class="ws-setting-val">${globalSettings["dashboard.companion_enabled"] ? "enabled" : "disabled"}</span>
          </div>
          <div class="ws-setting-row">
            <div class="ws-setting-info">
              <strong>디렉터 모드 (Tier-1 라이브 리액션)</strong>
              <span>유리 노크(깜짝·기상) + 스마트폰 알림 안무 + BoundedRotation 제어</span>
            </div>
            <span class="ws-setting-val">${escapeHtml(globalSettings["dashboard.companion_director"])}</span>
          </div>
          <div class="ws-setting-row">
            <div class="ws-setting-info">
              <strong>반응 쿨다운 (dashboard.companion_agent_cooldown_seconds)</strong>
              <span>메신저에서 "전역 설정 변경"으로 1-Click 승인 테스트 가능</span>
            </div>
            <span class="ws-setting-val">${escapeHtml(globalSettings["dashboard.companion_agent_cooldown_seconds"])}s</span>
          </div>
          <div class="ws-setting-row">
            <div class="ws-setting-info">
              <strong>메신저 고영향 승인 게이트</strong>
              <span>읽기·이동 즉시 실행, 전역 설정/일괄 모델 변경은 1-Click 승인</span>
            </div>
            <span class="ws-setting-val">confirm-high-impact</span>
          </div>
        </div>
      `;
      return;
    }

    workspaceContentEl.innerHTML = `
      <div class="ws-topbar">
        <div class="ws-title-group">
          <svg class="i" aria-hidden="true"><use href="#i-terminal"/></svg>
          <h1>${escapeHtml(active.title)}</h1>
          <span class="ws-route-chip">/s/${escapeHtml(active.id)}</span>
          <span class="ws-badge primary">${escapeHtml(active.model)}</span>
        </div>
        <div class="ws-accent-chip" title="세션별 노트북 리드 강조색 (FNV-1a)">
          <span class="ws-accent-swatch" aria-hidden="true"></span>
          <span>노트북 강조색 · ${escapeHtml(active.accent.label)}</span>
        </div>
      </div>

      <div class="ws-grid">
        <section class="ws-panel">
          <div class="ws-panel-head">
            <h2>활성 대화 스트림</h2>
            <span class="ws-badge ok">Bottom-Left 0px Jitter</span>
          </div>
          <div class="ws-trace-list">
            <article class="ws-trace-row">
              <div class="ws-trace-meta">
                <strong>Lead</strong>
                <span>whiteboard · companion-messenger-popup</span>
                <time>08:05</time>
              </div>
              <p class="ws-trace-body">
                (148)=a 좌측 상단 세션 레일 카드 아래에 <code>228px</code> 좌하단 컴패니언 전용 영역을 분리했습니다.
                <code>Unregistered workspaces</code>를 접고 펼쳐도 컴패니언 위치가 <code>0px</code>도 흔들리지 않습니다.
              </p>
            </article>
            <article class="ws-trace-row">
              <div class="ws-trace-meta">
                <strong>${escapeHtml(active.title)}</strong>
                <span>${escapeHtml(active.role)}</span>
                <time>${escapeHtml(active.updatedAt)}</time>
              </div>
              <p class="ws-trace-body">${escapeHtml(active.lastMsg)}</p>
            </article>
          </div>
        </section>

        <section class="ws-panel">
          <div class="ws-panel-head">
            <h2>워크스페이스 활성 에이전트</h2>
            <span class="ws-badge">${sessions.length} active</span>
          </div>
          <div class="ws-worker-grid">
            ${sessions
              .map(
                (s) => `
              <div class="ws-worker-row">
                <span class="ws-worker-name">
                  <span class="rs-dot${s.status === "working" ? " working" : ""}" style="--row-accent: ${escapeHtml(s.accent.hex)}"></span>
                  <span>${escapeHtml(s.title)}</span>
                </span>
                <span class="ws-worker-model${s.upgraded ? " upgraded" : ""}">${escapeHtml(s.model)}</span>
              </div>
            `
              )
              .join("")}
          </div>
        </section>
      </div>
    `;
  }

  function renderTier1Strip() {
    if (!messengerState) return;
    const t = messengerState.tier1;
    const mins = Math.floor(t.ttlRemainingSec / 60);
    const secs = String(t.ttlRemainingSec % 60).padStart(2, "0");
    tier1MetricsEl.textContent = `턴 ${t.turns}/${t.maxTurns} · ${(t.tokens / 1000).toFixed(1)}k/${(
      t.maxTokens / 1000
    ).toFixed(0)}k tok · TTL ${mins}m ${secs}s`;
    tier1SummaryEl.textContent = t.summary;
  }

  function renderConfirmCard(card) {
    const isPending = card.status === "pending";
    const isApproved = card.status === "approved";
    const headLabel = isPending
      ? "고영향 작업 · 1-Click 승인 필요"
      : isApproved
      ? "승인 및 실행 완료"
      : "사용자 취소됨";

    const headIcon = isPending ? "#i-shield" : isApproved ? "#i-check" : "#i-close";

    return `
      <div class="cm-confirm-card" data-confirm-card="${escapeHtml(card.id)}" data-status="${escapeHtml(card.status)}">
        <div class="cm-confirm-head">
          <svg class="i" aria-hidden="true"><use href="${headIcon}"/></svg>
          <span>${headLabel}</span>
          <span class="cm-confirm-tool">${escapeHtml(card.tool)}</span>
        </div>
        <div class="cm-confirm-body">
          <h4 class="cm-confirm-title">${escapeHtml(card.title)}</h4>
          <p class="cm-confirm-desc">${escapeHtml(card.description)}</p>
          <div class="cm-confirm-targets">
            ${card.targets.map((t) => `<span class="cm-target-chip">${escapeHtml(t)}</span>`).join("")}
          </div>
          ${
            isPending
              ? `
            <div class="cm-confirm-actions">
              <button type="button" class="cm-btn-approve" data-confirm-action="approve" data-card-id="${escapeHtml(card.id)}">
                <svg class="i" aria-hidden="true"><use href="#i-check"/></svg>
                <span>승인하고 실행</span>
              </button>
              <button type="button" class="cm-btn-cancel" data-confirm-action="cancel" data-card-id="${escapeHtml(card.id)}">취소</button>
            </div>
          `
              : `
            <div class="cm-confirm-receipt">
              <svg class="i" aria-hidden="true"><use href="${isApproved ? "#i-check" : "#i-close"}"/></svg>
              <span>${escapeHtml(card.receiptText || "")}</span>
            </div>
          `
          }
        </div>
      </div>
    `;
  }

  function renderMessages() {
    if (!messengerState || !workspaceSnapshot) return;
    const active = activeSession();

    const parts = [`<div class="cm-date-pill">오늘 · 2026년 11월 28일</div>`];

    messengerState.messages.forEach((msg, idx) => {
      const prev = messengerState.messages[idx - 1];
      const showHeader = msg.sender === "ai" && (!prev || prev.sender !== "ai");

      if (msg.sender === "user") {
        parts.push(`
          <div class="cm-msg user">
            <div class="cm-msg-col">
              <div class="cm-bubble-line">
                <div class="cm-bubble">${escapeHtml(msg.text)}</div>
                <span class="cm-time">${escapeHtml(msg.time)}</span>
              </div>
            </div>
          </div>
        `);
        return;
      }

      const pills = [];
      if (msg.reactionPill) {
        pills.push(
          `<span class="cm-react-pill mood-${escapeHtml(msg.reactionPill.mood || "ok")}">${escapeHtml(
            msg.reactionPill.icon
          )} ${escapeHtml(msg.reactionPill.label)}</span>`
        );
      }
      if (msg.toolBadge) {
        pills.push(`<span class="cm-tool-pill">${escapeHtml(msg.toolBadge)}</span>`);
      }

      parts.push(`
        <div class="cm-msg ai${showHeader ? "" : " is-cont"}">
          <div class="cm-msg-av${showHeader ? "" : " is-cont"}" aria-hidden="true">
            ${showHeader ? renderMiniAvatarSVG(msg.emotion || "warm", active.accent.hex) : ""}
          </div>
          <div class="cm-msg-col">
            ${showHeader ? `<span class="cm-sender">컴패니언</span>` : ""}
            <div class="cm-bubble-line">
              <div class="cm-bubble">${escapeHtml(msg.text)}</div>
              <span class="cm-time">${escapeHtml(msg.time)}</span>
            </div>
            ${pills.length ? `<div class="cm-meta-pills">${pills.join("")}</div>` : ""}
            ${msg.confirmCard ? renderConfirmCard(msg.confirmCard) : ""}
          </div>
        </div>
      `);
    });

    threadEl.innerHTML = parts.join("");
    threadEl.scrollTop = threadEl.scrollHeight;
  }

  function renderMessengerChrome() {
    if (!messengerState) return;
    popupEl.classList.toggle("is-closed", !messengerState.open);
    popupEl.setAttribute("aria-hidden", messengerState.open ? "false" : "true");

    pinBtn.setAttribute("aria-pressed", messengerState.pinned ? "true" : "false");

    if (messengerState.pose === "phone-buzz") {
      subtitleEl.textContent = "스마트폰 알림 울림 ♪ · 확인 준비 중";
    } else if (messengerState.pose === "phone-check") {
      subtitleEl.textContent = "한 손으로 스마트폰 확인 중…";
    } else if (messengerState.pose === "merong") {
      subtitleEl.textContent = "메-롱 😜 · 시답잖은 잡담 반응";
    } else if (messengerState.pose === "phone-facedown") {
      subtitleEl.textContent = "스마트폰 엎어두는 중 (탁!)";
    } else if (isBusy) {
      subtitleEl.textContent = "스마트폰으로 답장 작성 중…";
    } else if (messengerState.pose === "typing") {
      subtitleEl.textContent = "노트북 작업 중 · 메시지 보내면 스마트폰 확인";
    } else {
      subtitleEl.textContent = `${messengerState.stageCaption} · Tier-2 총괄`;
    }

    renderTier1Strip();
    syncPresetButtons();
  }

  function syncPresetButtons() {
    const presetBtns = document.querySelectorAll("[data-preview-presets] button");
    presetBtns.forEach((btn) => {
      const p = btn.dataset.preset;
      let active = false;
      if (p === "default") active = !messengerState.open && messengerState.pose === "typing";
      if (p === "sleepy") active = messengerState.pose === "sleepy";
      if (p === "open") active = messengerState.open && messengerState.pose !== "dizzy" && messengerState.pose !== "merong" && messengerState.pose !== "phone-facedown";
      if (p === "merong") active = messengerState.pose === "merong" || messengerState.pose === "phone-facedown";
      if (p === "dizzy") active = messengerState.pose === "dizzy";
      btn.setAttribute("aria-pressed", active ? "true" : "false");
    });
  }

  // ==========================================================================
  // Tactile Glass Knock FX ("톡톡" + expanding translucent ripple rings)
  // ==========================================================================
  function spawnGlassKnockFx(clientX, clientY) {
    const rect = slotEl.getBoundingClientRect();
    const x = Number.isFinite(clientX) ? clientX - rect.left : rect.width * 0.46;
    const y = Number.isFinite(clientY) ? clientY - rect.top : rect.height * 0.46;

    const ring1 = document.createElement("span");
    ring1.className = "knock-ring";
    ring1.style.left = `${x}px`;
    ring1.style.top = `${y}px`;

    const ring2 = document.createElement("span");
    ring2.className = "knock-ring ring-2";
    ring2.style.left = `${x}px`;
    ring2.style.top = `${y}px`;

    const word = document.createElement("span");
    word.className = "knock-word";
    word.textContent = Math.random() > 0.45 ? "톡톡" : "똑똑";
    word.style.left = `${Math.max(36, Math.min(rect.width - 36, x))}px`;
    word.style.top = `${Math.max(42, y - 10)}px`;

    knockLayerEl.append(ring1, ring2, word);

    setTimeout(() => {
      ring1.remove();
      ring2.remove();
      word.remove();
    }, 820);
  }

  function triggerDomScanPulse() {
    domScanEl.hidden = false;
    domScanEl.style.animation = "none";
    void domScanEl.offsetWidth;
    domScanEl.style.animation = "";
    setTimeout(() => {
      domScanEl.hidden = true;
    }, 980);
  }

  // ==========================================================================
  // Interactive Flows — Revision 2 Choreography
  // ==========================================================================

  // 1. Direct click on companion = PURE COSMETIC Glass Knock (NEVER opens messenger!)
  async function handleGlassKnock(evt) {
    const x = evt && Number.isFinite(evt.clientX) && evt.clientX > 0 ? evt.clientX : undefined;
    const y = evt && Number.isFinite(evt.clientY) && evt.clientY > 0 ? evt.clientY : undefined;
    spawnGlassKnockFx(x, y);

    const mySeq = ++choreoSeq;
    const res = await api.knockGlass();
    if (mySeq !== choreoSeq) return;

    // Notice messengerState.open is NEVER changed by knocking on the glass!
    messengerState.pose = res.pose;
    messengerState.emotion = res.emotion;
    messengerState.stageCaption = res.stageCaption;
    messengerState.stageQuip = res.stageQuip;
    messengerState.tier1 = res.tier1;

    renderStage();
    renderMessengerChrome();

    // Settle gracefully back to typing at laptop after the startled/wake-up/pout beat
    const settleDelay = res.reaction === "annoyed-tap" ? 2300 : res.reaction === "wake-jolt" ? 1650 : 1400;
    await sleep(settleDelay);
    if (mySeq !== choreoSeq) return;

    messengerState.pose = res.settlePose;
    messengerState.emotion = res.settleEmotion;
    messengerState.stageCaption = res.settleCaption;
    messengerState.stageQuip = "";
    await api.syncPoseState({
      pose: messengerState.pose,
      emotion: messengerState.emotion,
      stageCaption: messengerState.stageCaption,
      stageQuip: ""
    });
    renderStage();
    renderMessengerChrome();
  }

  // 2. Top-right speech-bubble icon = open/close messenger (does NOT drop laptop!)
  async function handleToggleMessenger(forceOpen) {
    const targetOpen = typeof forceOpen === "boolean" ? forceOpen : !messengerState.open;
    emojiPopEl.hidden = true;
    emojiBtn.setAttribute("aria-expanded", "false");

    const res = await api.setMessengerOpen(targetOpen);
    messengerState.open = res.open;
    messengerState.unreadCount = res.unreadCount;
    messengerState.pose = res.pose;
    messengerState.emotion = res.emotion;
    messengerState.stageCaption = res.stageCaption;
    messengerState.stageQuip = res.stageQuip;

    positionMessengerPopup();
    renderStage();
    renderMessengerChrome();

    if (messengerState.open) {
      threadEl.scrollTop = threadEl.scrollHeight;
      inputEl.focus();
    }
  }

  // 3. Send Message = Beat 1 (Phone Buzz 450ms) -> Beat 2 (One-Hand Check 650ms) -> Beat 3 (Smart Branch!)
  async function handleSendText(text) {
    const trimmed = String(text || "").trim();
    if (!trimmed || isBusy) return;

    emojiPopEl.hidden = true;
    emojiBtn.setAttribute("aria-expanded", "false");

    if (!messengerState.open) {
      await handleToggleMessenger(true);
    }

    const mySeq = ++choreoSeq;
    isBusy = true;
    inputEl.value = "";
    autoResizeInput();
    updateSendEnabled();

    // Optimistic user bubble
    const tempId = `opt-${Date.now()}`;
    messengerState.messages.push({
      id: tempId,
      sender: "user",
      text: trimmed,
      time: "전송 중"
    });
    renderMessages();

    // --- Beat 1: Smartphone Notification Alarm (~450ms) ---
    messengerState.pose = "phone-buzz";
    messengerState.emotion = "warm";
    messengerState.phoneFaceDown = false;
    messengerState.stageCaption = "스마트폰 알림 울림! ♪";
    messengerState.stageQuip = "어, 스마트폰 알림 왔네! 📱♪";
    typingEl.hidden = false;
    typingLabelEl.textContent = "스마트폰 알림 울림 ♪…";
    renderStage();
    renderMessengerChrome();

    await sleep(450);
    if (mySeq !== choreoSeq) {
      isBusy = false;
      typingEl.hidden = true;
      updateSendEnabled();
      return;
    }

    // --- Beat 2: One-Hand Lift & Check (~650ms) ---
    messengerState.pose = "phone-check";
    messengerState.emotion = "inspect";
    messengerState.stageCaption = "한 손으로 스마트폰 확인 중…";
    messengerState.stageQuip = "";
    typingLabelEl.textContent = "스마트폰 확인 중…";
    renderStage();
    renderMessengerChrome();

    const [res] = await Promise.all([api.sendMessage(trimmed), sleep(650)]);
    if (mySeq !== choreoSeq) {
      isBusy = false;
      typingEl.hidden = true;
      updateSendEnabled();
      return;
    }

    // Replace optimistic message with committed messages
    messengerState.messages = messengerState.messages.filter((m) => m.id !== tempId);
    isBusy = false;
    typingEl.hidden = true;
    updateSendEnabled();

    if (!res) {
      renderStage();
      renderMessages();
      renderMessengerChrome();
      return;
    }

    // --- Beat 3: Content-Aware Branching Reaction ---
    messengerState.messages.push(res.userMessage, res.assistantMessage);
    messengerState.pose = res.pose;
    messengerState.emotion = res.emotion;
    messengerState.stageCaption = res.stageCaption;
    messengerState.stageQuip = res.stageQuip;
    messengerState.tier1 = res.tier1;

    if (res.viewAction) {
      workspaceSnapshot.currentView = res.viewAction;
      renderWorkspace();
      showToast("Tier-2 ws/daemon.ui: 컴패니언 설정으로 이동했습니다");
    }
    if (res.inspectionPulse) {
      triggerDomScanPulse();
    }

    renderStage();
    renderMessages();
    renderMessengerChrome();

    if (res.flipFaceDown) {
      // Trivial fluff branch: Hold cheeky "메-롱!" expression -> flip phone face-down -> back to laptop!
      await sleep(1450);
      if (mySeq !== choreoSeq) return;

      messengerState.pose = "phone-facedown";
      messengerState.emotion = "merong";
      messengerState.phoneFaceDown = true;
      messengerState.stageCaption = "스마트폰 엎어놓는 중 (탁!)";
      messengerState.stageQuip = "시답잖은 잡담이라 폰 엎어뒀어요 😜";
      await api.syncPoseState({
        pose: messengerState.pose,
        emotion: messengerState.emotion,
        phoneFaceDown: true,
        stageCaption: messengerState.stageCaption,
        stageQuip: messengerState.stageQuip
      });
      renderStage();
      renderMessengerChrome();

      await sleep(1250);
      if (mySeq !== choreoSeq) return;

      messengerState.pose = "typing";
      messengerState.emotion = "calm";
      messengerState.phoneFaceDown = true;
      messengerState.stageCaption = res.settleCaption;
      messengerState.stageQuip = "";
      await api.syncPoseState({
        pose: "typing",
        emotion: "calm",
        phoneFaceDown: true,
        stageCaption: res.settleCaption,
        stageQuip: ""
      });
      renderStage();
      renderMessengerChrome();
    } else if (!res.holdPhoneOpen) {
      // Substantive / praise / annoyed branch: gently set phone back down beside laptop after idle beat
      await sleep(4200);
      if (mySeq !== choreoSeq) return;

      messengerState.pose = "typing";
      messengerState.emotion = "calm";
      messengerState.phoneFaceDown = false;
      messengerState.stageCaption = res.settleCaption;
      messengerState.stageQuip = "";
      await api.syncPoseState({
        pose: "typing",
        emotion: "calm",
        phoneFaceDown: false,
        stageCaption: res.settleCaption,
        stageQuip: ""
      });
      renderStage();
      renderMessengerChrome();
    }
  }

  async function handleConfirmDecision(cardId, decision) {
    if (isBusy) return;
    const mySeq = ++choreoSeq;
    isBusy = true;
    typingEl.hidden = false;
    typingLabelEl.textContent = "승인 결과 반영 중…";
    renderMessengerChrome();

    const res = await api.respondConfirmation(cardId, decision);
    if (res) {
      for (const m of messengerState.messages) {
        if (m.confirmCard && m.confirmCard.id === cardId) {
          m.confirmCard = res.card;
        }
      }
      messengerState.messages.push(res.receiptMessage);
      messengerState.pose = res.pose;
      messengerState.emotion = res.emotion;
      messengerState.stageCaption = res.stageCaption;
      messengerState.stageQuip = res.stageQuip;
      messengerState.tier1 = res.tier1;

      if (res.workspacePatch) {
        if (res.workspacePatch.sessions) {
          workspaceSnapshot.sessions = res.workspacePatch.sessions;
          renderRail();
        }
        if (res.workspacePatch.globalSettings) {
          workspaceSnapshot.globalSettings = res.workspacePatch.globalSettings;
        }
        renderWorkspace();
      }

      showToast(
        decision === "approve"
          ? "1-Click 승인 완료: 작업이 실행되었습니다"
          : "작업이 변경 없이 취소되었습니다"
      );
    }

    isBusy = false;
    typingEl.hidden = true;
    updateSendEnabled();
    renderStage();
    renderMessages();
    renderMessengerChrome();

    // Gently set phone back beside laptop after celebrating/cancelling
    if (res) {
      await sleep(3600);
      if (mySeq !== choreoSeq) return;
      messengerState.pose = "typing";
      messengerState.emotion = "calm";
      messengerState.stageCaption = res.settleCaption;
      messengerState.stageQuip = "";
      await api.syncPoseState({
        pose: "typing",
        emotion: "calm",
        stageCaption: res.settleCaption,
        stageQuip: ""
      });
      renderStage();
      renderMessengerChrome();
    }
  }

  async function handleSelectSession(sessionId) {
    if (sessionId === workspaceSnapshot.activeSessionId) return;
    const res = await api.selectSession(sessionId);
    if (!res) return;

    workspaceSnapshot.activeSessionId = res.activeSessionId;
    workspaceSnapshot.currentView = "session";
    messengerState.pose = res.pose;
    messengerState.emotion = res.emotion;
    messengerState.stageCaption = res.stageCaption;
    messengerState.stageQuip = res.stageQuip;
    messengerState.tier1 = res.tier1;

    slotEl.classList.remove("is-swapping");
    void slotEl.offsetWidth;
    slotEl.classList.add("is-swapping");
    clearTimeout(swapTimer);
    swapTimer = setTimeout(() => slotEl.classList.remove("is-swapping"), 580);

    renderRail();
    renderWorkspace();
    renderStage();
    renderMessengerChrome();
  }

  function autoResizeInput() {
    inputEl.style.height = "auto";
    inputEl.style.height = `${Math.min(92, Math.max(28, inputEl.scrollHeight))}px`;
  }

  function updateSendEnabled() {
    sendBtn.disabled = isBusy || !inputEl.value.trim();
  }

  // ==========================================================================
  // Event Bindings
  // ==========================================================================

  // Pure cosmetic glass knock on companion character
  hitBtn.addEventListener("click", (e) => {
    handleGlassKnock(e);
  });

  // Dedicated top-right speech-bubble button toggles messenger popup
  chatBtn.addEventListener("click", (e) => {
    e.stopPropagation();
    handleToggleMessenger();
  });

  rlistEl.addEventListener("click", (e) => {
    const row = e.target.closest("[data-session-id]");
    if (!row) return;
    handleSelectSession(row.dataset.sessionId);
  });

  unregBodyEl.addEventListener("click", (e) => {
    const item = e.target.closest("[data-unreg-name]");
    if (!item) return;
    showToast(`미등록 워크스페이스 선택: ${item.dataset.unregName}`);
  });

  // Theme switcher
  document.querySelector("[data-preview-theme]").addEventListener("click", (e) => {
    const btn = e.target.closest("[data-theme-btn]");
    if (!btn) return;
    const theme = btn.dataset.themeBtn;
    root.setAttribute("data-theme", theme);
    document.querySelectorAll("[data-theme-btn]").forEach((b) => {
      b.setAttribute("aria-pressed", b.dataset.themeBtn === theme ? "true" : "false");
    });
  });

  // Top Scenario Toolbar Presets
  document.querySelector("[data-preview-presets]").addEventListener("click", async (e) => {
    const btn = e.target.closest("[data-preset]");
    if (!btn) return;
    const preset = btn.dataset.preset;
    choreoSeq++;

    if (preset === "default") {
      const res = await api.resetDefaultDemo();
      Object.assign(messengerState, res);
      renderStage();
      renderMessengerChrome();
    } else if (preset === "sleepy") {
      const res = await api.triggerSleepyDemo();
      Object.assign(messengerState, res);
      renderStage();
      renderMessengerChrome();
    } else if (preset === "open") {
      await handleToggleMessenger(true);
    } else if (preset === "merong") {
      await handleSendText("뭐해 ㅋㅋ 심심한데 딴짓하지마~");
    } else if (preset === "confirm") {
      await handleSendText("활성 워커 전부 모델 opus-4.6으로 바꿔줘");
    } else if (preset === "dizzy") {
      const res = await api.triggerDizzyDemo();
      Object.assign(messengerState, res);
      renderStage();
      renderMessengerChrome();
    }
  });

  // Messenger popup clicks
  popupEl.addEventListener("click", async (e) => {
    const confirmBtn = e.target.closest("[data-confirm-action]");
    if (confirmBtn) {
      await handleConfirmDecision(confirmBtn.dataset.cardId, confirmBtn.dataset.confirmAction);
      return;
    }

    const chip = e.target.closest("[data-prompt]");
    if (chip) {
      await handleSendText(chip.dataset.prompt);
      return;
    }

    const quick = e.target.closest("[data-quick-send]");
    if (quick) {
      await handleSendText(quick.dataset.quickSend);
      return;
    }

    const actBtn = e.target.closest("[data-action]");
    if (!actBtn) return;
    const act = actBtn.dataset.action;

    if (act === "close-messenger") {
      await handleToggleMessenger(false);
    } else if (act === "toggle-pin") {
      const res = await api.setPinned(!messengerState.pinned);
      messengerState.pinned = res.pinned;
      renderMessengerChrome();
      showToast(res.pinned ? "메신저 팝업을 고정했습니다" : "메신저 팝업 고정을 해제했습니다");
    } else if (act === "toggle-tier1") {
      const open = tier1StripEl.hidden;
      tier1StripEl.hidden = !open;
      tier1Btn.setAttribute("aria-pressed", open ? "true" : "false");
      tier1Btn.setAttribute("aria-expanded", open ? "true" : "false");
    } else if (act === "reset-tier1") {
      messengerState.tier1 = await api.resetTier1Rotation();
      renderTier1Strip();
      showToast("Tier-1 BoundedRotation 컨텍스트를 리셋했습니다");
    } else if (act === "open-settings") {
      await api.navigateView("companion-settings");
      workspaceSnapshot.currentView = "companion-settings";
      renderWorkspace();
      showToast("컴패니언 설정 화면을 열었습니다");
    } else if (act === "toggle-emoji") {
      const willOpen = emojiPopEl.hidden;
      emojiPopEl.hidden = !willOpen;
      emojiBtn.setAttribute("aria-expanded", willOpen ? "true" : "false");
    } else if (act === "send-message") {
      await handleSendText(inputEl.value);
    }
  });

  // Workspace & Rail global actions
  document.addEventListener("click", async (e) => {
    const actBtn = e.target.closest(".rail-upper [data-action], .workspace-stage [data-action]");
    if (actBtn) {
      const act = actBtn.dataset.action;
      if (act === "open-settings") {
        await api.navigateView("companion-settings");
        workspaceSnapshot.currentView = "companion-settings";
        renderWorkspace();
      } else if (act === "back-to-session") {
        await api.navigateView("session");
        workspaceSnapshot.currentView = "session";
        renderWorkspace();
      } else if (act === "rail-refresh") {
        showToast("세션 레일 동기화 완료");
      } else if (act === "rail-add") {
        showToast("새 세션 생성 (목업 프리뷰)");
      }
      return;
    }

    // Outside click closes messenger when unpinned
    if (
      messengerState &&
      messengerState.open &&
      !messengerState.pinned &&
      !e.target.closest("[data-messenger-popup]") &&
      !e.target.closest("#rail-companion-slot") &&
      !e.target.closest("#rws-unreg") &&
      !e.target.closest(".preview-bar") &&
      !e.target.closest("[data-session-id]")
    ) {
      await handleToggleMessenger(false);
    }
  });

  // Composer input keyboard & autosize
  inputEl.addEventListener("input", () => {
    autoResizeInput();
    updateSendEnabled();
  });

  inputEl.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey && !e.isComposing) {
      e.preventDefault();
      if (!sendBtn.disabled) {
        handleSendText(inputEl.value);
      }
    }
  });

  // Escape closes unpinned messenger or emoji popover
  document.addEventListener("keydown", async (e) => {
    if (e.key === "Escape") {
      if (!emojiPopEl.hidden) {
        emojiPopEl.hidden = true;
        emojiBtn.setAttribute("aria-expanded", "false");
        return;
      }
      if (messengerState && messengerState.open) {
        await handleToggleMessenger(false);
      }
    }
  });

  window.addEventListener("resize", () => {
    positionMessengerPopup();
  });

  // ==========================================================================
  // Boot
  // ==========================================================================
  async function boot() {
    const [ws, ms] = await Promise.all([api.getWorkspaceSnapshot(), api.getMessengerState()]);
    workspaceSnapshot = ws;
    messengerState = ms;

    renderRail();
    renderWorkspace();
    renderStage();
    renderMessages();
    renderMessengerChrome();
    positionMessengerPopup();

    requestAnimationFrame(positionMessengerPopup);
  }

  boot();
})();
