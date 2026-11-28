(function (global) {
  "use strict";

  // ==========================================================================
  // Portable Mock API Adapter for Werkschaft Step 4 Companion Messenger
  // Revision 1 + Revision 2:
  //   - Bottom-left rail ownership (zero jitter on Unregistered workspaces toggle)
  //   - Direct companion click = pure cosmetic glass knock (startled / wake-up jolt)
  //   - Top-right speech-bubble button = open/close messenger without dropping laptop
  //   - Message send = smartphone buzz -> one-hand lift & check -> branching reaction
  //     (substantive reply / 1-click confirmation / trivial "메-롱" + face-down flip)
  // ==========================================================================

  const SESSION_ACCENT_PALETTE = Object.freeze([
    Object.freeze({ id: "sapphire",  label: "Sapphire",  hex: "#5b9eff", rim: "#a5cbff" }),
    Object.freeze({ id: "lavender",  label: "Lavender",  hex: "#a78bfa", rim: "#d2c3ff" }),
    Object.freeze({ id: "mint-teal", label: "Mint Teal", hex: "#34d399", rim: "#96f0ce" }),
    Object.freeze({ id: "amber",     label: "Amber",     hex: "#f59e0b", rim: "#fcd37a" }),
    Object.freeze({ id: "coral",     label: "Coral",     hex: "#fb7185", rim: "#ffb3bf" }),
    Object.freeze({ id: "ice-cyan",  label: "Ice Cyan",  hex: "#38bdf8", rim: "#9be3ff" })
  ]);

  function sessionAccentForKey(sessionKey) {
    const raw = typeof sessionKey === "string" ? sessionKey.trim() : "";
    if (!raw) return SESSION_ACCENT_PALETTE[0];
    let h = 2166136261 >>> 0;
    for (let i = 0; i < raw.length; i++) {
      h ^= raw.charCodeAt(i);
      h = Math.imul(h, 16777619) >>> 0;
    }
    return SESSION_ACCENT_PALETTE[(h >>> 0) % SESSION_ACCENT_PALETTE.length];
  }

  function nowLabel() {
    const d = new Date();
    const h = d.getHours();
    const m = String(d.getMinutes()).padStart(2, "0");
    const period = h < 12 ? "오전" : "오후";
    const h12 = h % 12 === 0 ? 12 : h % 12;
    return `${period} ${h12}:${m}`;
  }

  function clone(val) {
    return JSON.parse(JSON.stringify(val));
  }

  function createMockApi() {
    let seq = 100;
    const nextId = (prefix) => `${prefix}-${++seq}`;

    const sessions = [
      {
        id: "lead-companion-design",
        title: "lead-companion-design",
        role: "Lead",
        model: "opus-4.6",
        status: "working",
        lastMsg: "Step 4 좌하단 고정 및 스마트폰 알림 안무 검토 중",
        updatedAt: "방금",
        accent: SESSION_ACCENT_PALETTE[0]
      },
      {
        id: "worker-vrm-colead",
        title: "worker-vrm-colead",
        role: "Worker",
        model: "sonnet-4.6",
        status: "working",
        lastMsg: "유리 노크 깜짝/기상 포즈 · 한 손 폰 확인 전환 구현",
        updatedAt: "1m",
        accent: SESSION_ACCENT_PALETTE[1]
      },
      {
        id: "worker-ephemeral-pool",
        title: "worker-ephemeral-pool",
        role: "Worker",
        model: "sonnet-4.6",
        status: "idle",
        lastMsg: "BoundedRotation 턴/시간/토큰 캡 단위 테스트 통과",
        updatedAt: "4m",
        accent: SESSION_ACCENT_PALETTE[2]
      },
      {
        id: "review-sweep-053",
        title: "review-sweep-053",
        role: "Reviewer",
        model: "codex-5.3",
        status: "waiting",
        lastMsg: "develop 브랜치 워터마크 검수 대기",
        updatedAt: "9m",
        accent: SESSION_ACCENT_PALETTE[3]
      }
    ];

    const unregistered = [
      { name: "sandbox-vrm-assets", path: "~/work/sandbox-vrm-assets", count: 1 },
      { name: "docs-scratch", path: "~/work/docs-scratch", count: 2 },
      { name: "playground-shader-lab", path: "~/work/playground-shader-lab", count: 1 }
    ];

    const globalSettings = {
      "dashboard.companion_enabled": true,
      "dashboard.companion_director": "agent",
      "dashboard.companion_agent_cooldown_seconds": 30,
      "dashboard.companion_agent_hourly_cap": 20,
      "dashboard.companion_messenger_gate": "confirm-high-impact"
    };

    const state = {
      workspace: {
        name: "devenv",
        path: "~/devenv",
        branch: "develop",
        gitClean: "+0 ~2"
      },
      activeSessionId: "lead-companion-design",
      currentView: "session", // "session" | "companion-settings"
      messengerOpen: false,
      pinned: false,
      unreadCount: 1,
      pose: "typing", // typing | sleepy | startled | wake-jolt | phone-buzz | phone-check | smartphone-peek | merong | phone-facedown | laugh | annoyed-tap | dizzy
      emotion: "calm",
      phoneFaceDown: false,
      stageCaption: "노트북 작업 중 · Sapphire",
      stageQuip: "",
      rapidSwitchTimes: [],
      knockTimes: [],
      tier1: {
        turns: 3,
        maxTurns: 12,
        tokens: 1240,
        maxTokens: 4000,
        ttlRemainingSec: 258,
        summary: "좌하단 컴패니언 고정 슬롯 대기 중 · 유리 노크(코스메틱) / 우상단 💬(메신저) 분리 완료"
      },
      messages: [
        {
          id: "m-1",
          sender: "ai",
          text: "안녕하세요! 메시지 보내시면 옆에 둔 스마트폰 알림이 울리고 한 손으로 들어서 바로 확인해 드릴게요. 유리창을 직접 톡톡 두드리면 깜짝 놀라거나 졸다가 깨는 반응만 보여드려요!",
          time: "오전 8:02",
          emotion: "warm",
          reactionPill: { icon: "📱", label: "알림 대기 중", mood: "ok" }
        },
        {
          id: "m-2",
          sender: "user",
          text: "지금 워크스페이스에 활성 워커 몇 개 돌아가고 있는지 잠깐 봐줄래?",
          time: "오전 8:04"
        },
        {
          id: "m-3",
          sender: "ai",
          text: "바로 확인했어요! 현재 devenv (develop) 기준 리드 1명과 워커·리뷰어 3명이 열려 있고, worker-vrm-colead가 작업 중이에요. 읽기 전용 조회라 별도 확인 없이 바로 읽어왔습니다.",
          time: "오전 8:04",
          emotion: "inspect",
          reactionPill: { icon: "👀", label: "화면 확인", mood: "inspect" },
          toolBadge: "ws/daemon.dom · 38ms"
        }
      ]
    };

    function getActiveSession() {
      return sessions.find((s) => s.id === state.activeSessionId) || sessions[0];
    }

    function defaultTypingCaption() {
      const cur = getActiveSession();
      return `노트북 작업 중 · ${cur.accent.label}`;
    }

    function bumpTier1(summaryText, tokenDelta) {
      state.tier1.turns += 1;
      state.tier1.tokens += tokenDelta || 260;
      if (state.tier1.turns > state.tier1.maxTurns || state.tier1.tokens > state.tier1.maxTokens) {
        state.tier1.turns = 1;
        state.tier1.tokens = 420;
        state.tier1.ttlRemainingSec = 300;
      }
      if (summaryText) {
        state.tier1.summary = summaryText;
      }
    }

    function findCard(cardId) {
      for (const msg of state.messages) {
        if (msg.confirmCard && msg.confirmCard.id === cardId) {
          return msg.confirmCard;
        }
      }
      return null;
    }

    function buildReplyForInput(rawText) {
      const text = rawText.trim();
      const lower = text.toLowerCase();

      // 1. High-impact: Bulk worker model switch -> 1-Click Confirmation Card
      if (
        (lower.includes("워커") || lower.includes("모델") || lower.includes("worker") || lower.includes("model")) &&
        (lower.includes("바꿔") || lower.includes("변경") || lower.includes("일괄") || lower.includes("switch") || lower.includes("전부"))
      ) {
        const targets = sessions.filter((s) => s.role !== "Lead").map((s) => s.id);
        const card = {
          id: nextId("gate"),
          kind: "bulk_model_switch",
          status: "pending",
          title: `활성 워커 ${targets.length}개 모델 일괄 변경`,
          description: "진행 중인 워커·리뷰어 세션의 모델을 opus-4.6으로 전환합니다. 고영향 변경이므로 승인 후에만 실행됩니다.",
          tool: "ws/daemon.curl",
          endpoint: "POST /agents/bulk-model",
          targetModel: "opus-4.6",
          targets
        };
        return {
          branch: "substantive",
          pose: "smartphone-peek",
          emotion: "focused",
          holdPhoneOpen: true,
          flipFaceDown: false,
          stageCaption: "1-Click 승인 대기 중",
          stageQuip: "고영향 변경이라 승인 카드 띄웠어요!",
          tier1Summary: `스마트폰 확인 → 워커 ${targets.length}개 일괄 모델 변경 요청 · 1-Click 승인 대기 중`,
          assistantMessage: {
            id: nextId("m"),
            sender: "ai",
            text: "활성 워커 전체 모델 변경은 고영향 작업이라 바로 실행하지 않고 승인 카드를 준비했어요. 대상 확인하시고 한 번만 눌러주세요!",
            time: nowLabel(),
            emotion: "focused",
            reactionPill: { icon: "🛡️", label: "승인 게이트", mood: "annoyed" },
            confirmCard: card
          }
        };
      }

      // 2. High-impact: Global config write -> 1-Click Confirmation Card
      if (
        (lower.includes("전역") || lower.includes("쿨다운") || lower.includes("cooldown") || lower.includes("config")) &&
        (lower.includes("바꿔") || lower.includes("변경") || lower.includes("설정") || lower.includes("set")) &&
        !lower.includes("열어")
      ) {
        const card = {
          id: nextId("gate"),
          kind: "global_config_write",
          status: "pending",
          title: "전역 컴패니언 반응 쿨다운 변경",
          description: "전역 ~/.ws/config.json의 dashboard.companion_agent_cooldown_seconds 값을 30초 → 15초로 저장합니다.",
          tool: "ws/daemon.curl",
          endpoint: "POST /api/config/set",
          configKey: "dashboard.companion_agent_cooldown_seconds",
          configValue: 15,
          targets: ["~/.ws/config.json", "cooldown: 30s → 15s"]
        };
        return {
          branch: "substantive",
          pose: "smartphone-peek",
          emotion: "focused",
          holdPhoneOpen: true,
          flipFaceDown: false,
          stageCaption: "1-Click 승인 대기 중",
          stageQuip: "전역 설정 저장 전 확인 부탁해요!",
          tier1Summary: "스마트폰 확인 → 전역 컴패니언 쿨다운 15초 변경 요청 · 1-Click 승인 대기",
          assistantMessage: {
            id: nextId("m"),
            sender: "ai",
            text: "전역 설정 파일(~/.ws/config.json)을 수정하는 작업이에요. 아래 카드에서 승인해주시면 바로 반영할게요!",
            time: nowLabel(),
            emotion: "focused",
            reactionPill: { icon: "⚙️", label: "전역 설정 게이트", mood: "annoyed" },
            confirmCard: card
          }
        };
      }

      // 3. Non-destructive UI Navigation: Open companion settings (immediate)
      if (
        (lower.includes("설정") || lower.includes("settings")) &&
        (lower.includes("열어") || lower.includes("이동") || lower.includes("보여") || lower.includes("open"))
      ) {
        state.currentView = "companion-settings";
        return {
          branch: "substantive",
          pose: "smartphone-peek",
          emotion: "ok",
          holdPhoneOpen: false,
          flipFaceDown: false,
          stageCaption: "설정 화면으로 이동 완료",
          stageQuip: "컴패니언 설정 바로 열어드렸어요!",
          viewAction: "companion-settings",
          tier1Summary: "스마트폰 확인 → /settings?section=companion 즉시 이동 완료",
          assistantMessage: {
            id: nextId("m"),
            sender: "ai",
            text: "컴패니언 설정 화면(/settings?section=companion)으로 바로 열어드렸어요! 비파괴 화면 이동이라 별도 승인 없이 즉시 처리했습니다.",
            time: nowLabel(),
            emotion: "ok",
            reactionPill: { icon: "✨", label: "즉시 이동", mood: "ok" },
            toolBadge: "ws/daemon.ui · navigate"
          }
        };
      }

      // 4. Read-only screen inspection (immediate + DOM scan sweep)
      if (
        lower.includes("화면") || lower.includes("뭐가 떠") || lower.includes("보여줘") ||
        lower.includes("요약") || lower.includes("screen") || lower.includes("상태")
      ) {
        const cur = getActiveSession();
        const viewName = state.currentView === "companion-settings"
          ? "워크플로 설정 (/settings?section=companion)"
          : `세션 보드 (${cur.title} · ${cur.model})`;
        return {
          branch: "substantive",
          pose: "smartphone-peek",
          emotion: "inspect",
          holdPhoneOpen: false,
          flipFaceDown: false,
          stageCaption: "현재 화면 읽는 중",
          stageQuip: "화면 슥 훑어보고 정리했어요 👀",
          inspectionPulse: true,
          tier1Summary: `스마트폰 확인 → 현재 화면(${viewName}) DOM/스크린샷 읽기 완료`,
          assistantMessage: {
            id: nextId("m"),
            sender: "ai",
            text: `지금 오른쪽 메인 스테이지에는 [${viewName}]이 열려 있어요. 활성 세션은 총 ${sessions.length}개이고, 현재 선택된 노트북 강조색은 ${cur.accent.label}입니다. 읽기 전용 브리지라 확인 창 없이 바로 읽었어요!`,
            time: nowLabel(),
            emotion: "inspect",
            reactionPill: { icon: "👀", label: "화면 확인", mood: "inspect" },
            toolBadge: "ws/daemon.dom + screenshot · 64ms"
          }
        };
      }

      // 5. Trivial / silly / casual fluff -> Cheeky "메-롱!" + Flip smartphone face-down onto desk!
      if (
        lower.includes("뭐해") || lower.includes("심심") || lower.includes("배고파") ||
        lower.includes("배고프") || lower.includes("딴짓하지마") || lower.includes("메롱") ||
        lower.includes("메-롱") || lower.includes("시답잖") || lower.includes("놀아줘") ||
        lower.includes("잡담") || lower.includes("점심 뭐") || lower.includes("저녁 뭐") ||
        lower.includes("아무말") || lower.includes("ㅋㅋㅋ")
      ) {
        return {
          branch: "trivial",
          pose: "merong",
          emotion: "merong",
          holdPhoneOpen: false,
          flipFaceDown: true,
          stageCaption: "메-롱! 😜 (시답잖은 잡담)",
          stageQuip: "메-롱 😜 중요한 건 줄 알았잖아요~",
          tier1Summary: "시답잖은 잡담 수신 → '메-롱 😜' 반응 후 스마트폰 엎어두고 노트북 복귀",
          assistantMessage: {
            id: nextId("m"),
            sender: "ai",
            text: "에이, 뭐 중요한 건 줄 알고 한 손으로 폰 들었더니~ 메-롱 😜 저 바쁘니까 폰 엎어두고 다시 노트북 볼게요!",
            time: nowLabel(),
            emotion: "merong",
            reactionPill: { icon: "😜", label: "메-롱! · 폰 엎어둠", mood: "merong" }
          }
        };
      }

      // 6. Repeated nagging / playful tease -> Annoyed tap pout
      if (
        lower.includes("잔소리") || lower.includes("빨리") || lower.includes("일해") ||
        lower.includes("톡톡") || lower.includes("놀리") || lower.includes("딴짓") ||
        lower.includes("에베베") || lower.includes("삐져") || lower.includes("찌르") || lower.includes("poke")
      ) {
        return {
          branch: "annoyed",
          pose: "annoyed-tap",
          emotion: "annoyed",
          holdPhoneOpen: false,
          flipFaceDown: false,
          stageCaption: "유리 톡톡 삐짐 모드",
          stageQuip: "흥, 딴짓 아니고 빌드 보는 거였거든요!",
          tier1Summary: "사용자의 장난스러운 잔소리 감지 · 감정: annoyed-tap",
          assistantMessage: {
            id: nextId("m"),
            sender: "ai",
            text: "흥! 딴짓 아니거든요? 옆에서 로그랑 테스트 꼼꼼히 보고 있었단 말이에요… 자꾸 잔소리하시면 저도 유리창 안에서 톡톡 두드릴 거예요! 😤",
            time: nowLabel(),
            emotion: "annoyed",
            reactionPill: { icon: "😤", label: "유리 톡톡", mood: "annoyed" }
          }
        };
      }

      // 7. Praise / joke / coffee -> Cute laugh reaction
      if (
        lower.includes("고마") || lower.includes("최고") || lower.includes("잘했") ||
        lower.includes("ㅎㅎ") || lower.includes("ㅋㅋ") || lower.includes("웃") ||
        lower.includes("커피") || lower.includes("칭찬") || lower.includes("예쁘") ||
        lower.includes("귀여") || lower.includes("수고")
      ) {
        return {
          branch: "praise",
          pose: "laugh",
          emotion: "laugh",
          holdPhoneOpen: false,
          flipFaceDown: false,
          stageCaption: "기분 좋아서 웃는 중",
          stageQuip: "헤헤, 그렇게 말씀해 주시니까 힘나요!",
          tier1Summary: "사용자의 칭찬/응원으로 분위기 매우 밝음 · 감정: laugh (행복)",
          assistantMessage: {
            id: nextId("m"),
            sender: "ai",
            text: "헤헤, 그렇게 말씀해 주시니까 갑자기 텐션 확 올라가네요! 옆 유리창 너머에서 계속 든든하게 지켜보고 있을게요 😊",
            time: nowLabel(),
            emotion: "laugh",
            reactionPill: { icon: "😊", label: "웃음", mood: "laugh" }
          }
        };
      }

      // 8. Default substantive Co-Lead work response
      const cur = getActiveSession();
      return {
        branch: "substantive",
        pose: "smartphone-peek",
        emotion: "warm",
        holdPhoneOpen: false,
        flipFaceDown: false,
        stageCaption: "스마트폰으로 답장 완료",
        stageQuip: "알겠어요, 같이 살펴볼게요!",
        tier1Summary: `스마트폰 확인 → ${cur.title} 기준 Co-Lead 응답 전달 완료`,
        assistantMessage: {
          id: nextId("m"),
          sender: "ai",
          text: `"${text}" 확인했어요! 지금 보고 계신 ${cur.title} 컨텍스트 기준으로 바로 화면을 읽어드리거나, 필요하면 워커들에게 지시를 전달해 드릴 수 있어요.`,
          time: nowLabel(),
          emotion: "warm",
          reactionPill: { icon: "💛", label: "Co-Lead 응답", mood: "ok" }
        }
      };
    }

    return {
      async getWorkspaceSnapshot() {
        return clone({
          workspace: state.workspace,
          sessions,
          activeSessionId: state.activeSessionId,
          unregistered,
          currentView: state.currentView,
          globalSettings
        });
      },

      async selectSession(sessionId) {
        const target = sessions.find((s) => s.id === sessionId);
        if (!target) return null;
        state.activeSessionId = target.id;

        const now = Date.now();
        state.rapidSwitchTimes.push(now);
        state.rapidSwitchTimes = state.rapidSwitchTimes.filter((t) => now - t <= 2800);

        let dizzyTriggered = false;
        if (state.rapidSwitchTimes.length >= 4) {
          dizzyTriggered = true;
          state.rapidSwitchTimes = [];
          state.pose = "dizzy";
          state.emotion = "dizzy";
          state.stageCaption = "어지러워요! (세션 급전환)";
          state.stageQuip = "으아~ 노트북을 너무 빨리 바꿔서 어지러워요!";
          bumpTier1("세션 4회 급전환 감지 · Tier-1 dizzy 리액션 발동", 120);
        } else if (state.pose === "typing" || state.pose === "dizzy") {
          state.pose = "typing";
          state.emotion = "calm";
          state.stageCaption = defaultTypingCaption();
          state.stageQuip = "";
        }

        return clone({
          activeSessionId: state.activeSessionId,
          session: target,
          accent: target.accent,
          dizzyTriggered,
          pose: state.pose,
          emotion: state.emotion,
          phoneFaceDown: state.phoneFaceDown,
          stageCaption: state.stageCaption,
          stageQuip: state.stageQuip,
          tier1: state.tier1
        });
      },

      async getMessengerState() {
        return clone({
          open: state.messengerOpen,
          pinned: state.pinned,
          unreadCount: state.unreadCount,
          pose: state.pose,
          emotion: state.emotion,
          phoneFaceDown: state.phoneFaceDown,
          stageCaption: state.stageCaption,
          stageQuip: state.stageQuip,
          messages: state.messages,
          tier1: state.tier1,
          currentView: state.currentView
        });
      },

      // Revision 2: Pure cosmetic Glass Knock — NEVER opens or closes the messenger!
      async knockGlass() {
        const now = Date.now();
        state.knockTimes.push(now);
        state.knockTimes = state.knockTimes.filter((t) => now - t <= 3200);

        const wasSleepy = state.pose === "sleepy";
        let reaction = "startled";

        if (wasSleepy) {
          state.knockTimes = [];
          reaction = "wake-jolt";
          state.pose = "wake-jolt";
          state.emotion = "startled";
          state.stageCaption = "화들짝 기상!";
          state.stageQuip = "으앗, 안 잤어요!";
          bumpTier1("졸림 상태에서 유리 노크 → 화들짝 깨어나 노트북 복귀", 95);
        } else if (state.knockTimes.length >= 3) {
          state.knockTimes = [];
          reaction = "annoyed-tap";
          state.pose = "annoyed-tap";
          state.emotion = "annoyed";
          state.stageCaption = "유리 톡톡 삐짐";
          state.stageQuip = "자꾸 두드리면 유리 지문 묻어요! 😤";
          bumpTier1("유리창 연속 노크 감지 → 장난스러운 삐짐 (메신저 미개방)", 110);
        } else {
          reaction = "startled";
          state.pose = "startled";
          state.emotion = "startled";
          state.stageCaption = "엇, 깜짝이야!";
          state.stageQuip = "엇, 깜짝이야!";
          bumpTier1("유리창 코스메틱 노크 → 깜짝 놀람 반응 후 노트북 복귀", 80);
        }

        return clone({
          reaction,
          wasSleepy,
          open: state.messengerOpen, // unchanged!
          unreadCount: state.unreadCount,
          pose: state.pose,
          emotion: state.emotion,
          phoneFaceDown: state.phoneFaceDown,
          stageCaption: state.stageCaption,
          stageQuip: state.stageQuip,
          settlePose: "typing",
          settleEmotion: "calm",
          settleCaption: wasSleepy ? `노트북 복귀 · ${getActiveSession().accent.label}` : defaultTypingCaption(),
          tier1: state.tier1
        });
      },

      // Revision 2: Dedicated top-right speech-bubble button opens/closes messenger
      // without forcing her to drop her laptop!
      async setMessengerOpen(nextOpen) {
        state.messengerOpen = !!nextOpen;
        if (state.messengerOpen) {
          state.unreadCount = 0;
          // Stay at her laptop (or current resting pose) until a message is actually sent!
          if (state.pose === "dizzy") {
            state.pose = "typing";
            state.emotion = "calm";
            state.stageCaption = defaultTypingCaption();
            state.stageQuip = "";
          }
        } else {
          if (
            state.pose === "smartphone-peek" ||
            state.pose === "phone-check" ||
            state.pose === "phone-buzz" ||
            state.pose === "laugh" ||
            state.pose === "annoyed-tap"
          ) {
            state.pose = "typing";
            state.emotion = "calm";
            state.stageCaption = defaultTypingCaption();
            state.stageQuip = "";
          }
        }

        return clone({
          open: state.messengerOpen,
          unreadCount: state.unreadCount,
          pose: state.pose,
          emotion: state.emotion,
          phoneFaceDown: state.phoneFaceDown,
          stageCaption: state.stageCaption,
          stageQuip: state.stageQuip,
          tier1: state.tier1
        });
      },

      async sendMessage(rawText) {
        const text = String(rawText || "").trim();
        if (!text) return null;

        const userMessage = {
          id: nextId("u"),
          sender: "user",
          text,
          time: nowLabel()
        };
        state.messages.push(userMessage);

        // Short coordinator thinking budget (runs concurrently with Beat 2 one-hand check)
        await new Promise((r) => setTimeout(r, 120));

        const reply = buildReplyForInput(text);
        state.pose = reply.pose;
        state.emotion = reply.emotion;
        state.phoneFaceDown = false;
        state.stageCaption = reply.stageCaption;
        state.stageQuip = reply.stageQuip || "";
        state.messages.push(reply.assistantMessage);
        bumpTier1(reply.tier1Summary, 290);

        return clone({
          branch: reply.branch,
          userMessage,
          assistantMessage: reply.assistantMessage,
          pose: state.pose,
          emotion: state.emotion,
          holdPhoneOpen: !!reply.holdPhoneOpen,
          flipFaceDown: !!reply.flipFaceDown,
          stageCaption: state.stageCaption,
          stageQuip: state.stageQuip,
          settleCaption: defaultTypingCaption(),
          viewAction: reply.viewAction || null,
          inspectionPulse: !!reply.inspectionPulse,
          tier1: state.tier1
        });
      },

      async syncPoseState(patch) {
        if (!patch || typeof patch !== "object") return;
        if (patch.pose) state.pose = patch.pose;
        if (patch.emotion) state.emotion = patch.emotion;
        if (typeof patch.phoneFaceDown === "boolean") state.phoneFaceDown = patch.phoneFaceDown;
        if (typeof patch.stageCaption === "string") state.stageCaption = patch.stageCaption;
        if (typeof patch.stageQuip === "string") state.stageQuip = patch.stageQuip;
      },

      async respondConfirmation(cardId, decision) {
        const card = findCard(cardId);
        if (!card || card.status !== "pending") return null;

        await new Promise((r) => setTimeout(r, 220));

        let receiptMessage;
        let workspacePatch = null;

        if (decision === "approve") {
          card.status = "approved";
          card.resolvedAt = nowLabel();
          if (card.kind === "bulk_model_switch") {
            sessions.forEach((s) => {
              if (s.role !== "Lead") {
                s.model = card.targetModel;
                s.upgraded = true;
              }
            });
            card.receiptText = `실행 완료 · 워커 ${card.targets.length}개 → ${card.targetModel} (${card.resolvedAt})`;
            workspacePatch = { sessions: clone(sessions) };
            receiptMessage = {
              id: nextId("m"),
              sender: "ai",
              text: `승인 감사해요! 활성 워커 ${card.targets.length}개 모두 ${card.targetModel} 모델로 깔끔하게 전환 완료했습니다. 왼쪽 세션 레일 모델 배지도 바로 갱신됐어요 ✨`,
              time: nowLabel(),
              emotion: "laugh",
              reactionPill: { icon: "✨", label: "일괄 실행 완료", mood: "ok" },
              toolBadge: "ws/daemon.curl · 200 OK"
            };
          } else {
            globalSettings[card.configKey] = card.configValue;
            card.receiptText = `저장 완료 · ${card.configKey} = ${card.configValue}s (${card.resolvedAt})`;
            workspacePatch = { globalSettings: clone(globalSettings) };
            receiptMessage = {
              id: nextId("m"),
              sender: "ai",
              text: "전역 컴패니언 반응 쿨다운을 15초로 저장했어요! 이제 조금 더 경쾌하게 옆에서 반응해 드릴게요 😊",
              time: nowLabel(),
              emotion: "laugh",
              reactionPill: { icon: "✨", label: "설정 반영됨", mood: "ok" },
              toolBadge: "ws/daemon.curl · 200 OK"
            };
          }
          state.pose = "laugh";
          state.emotion = "laugh";
          state.stageCaption = "실행 완료 · 기분 좋음";
          state.stageQuip = "승인된 작업 깔끔하게 끝냈어요!";
          bumpTier1(`1-Click 승인 실행 완료 (${card.title}) · 상태: 정상`, 240);
        } else {
          card.status = "cancelled";
          card.resolvedAt = nowLabel();
          card.receiptText = `취소됨 · 시스템 변경 없음 (${card.resolvedAt})`;
          receiptMessage = {
            id: nextId("m"),
            sender: "ai",
            text: "알겠어요, 해당 작업은 실행하지 않고 그대로 취소했습니다! 안전하게 유지해 둘게요.",
            time: nowLabel(),
            emotion: "warm",
            reactionPill: { icon: "👌", label: "안전 취소", mood: "inspect" }
          };
          state.pose = "smartphone-peek";
          state.emotion = "warm";
          state.stageCaption = "안전 취소 완료";
          state.stageQuip = "변경 없이 안전하게 취소했어요.";
          bumpTier1(`1-Click 승인 카드 사용자 취소 (${card.title}) · 변경 없음`, 150);
        }

        state.messages.push(receiptMessage);

        return clone({
          card,
          receiptMessage,
          workspacePatch,
          pose: state.pose,
          emotion: state.emotion,
          stageCaption: state.stageCaption,
          stageQuip: state.stageQuip,
          settleCaption: defaultTypingCaption(),
          tier1: state.tier1
        });
      },

      async setPinned(pinned) {
        state.pinned = !!pinned;
        return { pinned: state.pinned };
      },

      async resetDefaultDemo() {
        state.messengerOpen = false;
        state.unreadCount = 1;
        state.pose = "typing";
        state.emotion = "calm";
        state.phoneFaceDown = false;
        state.stageCaption = defaultTypingCaption();
        state.stageQuip = "";
        return clone({
          open: state.messengerOpen,
          unreadCount: state.unreadCount,
          pose: state.pose,
          emotion: state.emotion,
          phoneFaceDown: state.phoneFaceDown,
          stageCaption: state.stageCaption,
          stageQuip: state.stageQuip,
          tier1: state.tier1
        });
      },

      async triggerSleepyDemo() {
        state.messengerOpen = false;
        state.pose = "sleepy";
        state.emotion = "sleepy";
        state.stageCaption = "꾸벅꾸벅 졸고 있음 (zZz)";
        state.stageQuip = "꾸벅꾸벅… 유리를 톡톡 두드려 깨워보세요!";
        bumpTier1("졸림 상태 진입 · 유리 노크로 화들짝 기상 테스트 대기", 80);
        return clone({
          open: state.messengerOpen,
          unreadCount: state.unreadCount,
          pose: state.pose,
          emotion: state.emotion,
          phoneFaceDown: state.phoneFaceDown,
          stageCaption: state.stageCaption,
          stageQuip: state.stageQuip,
          tier1: state.tier1
        });
      },

      async triggerDizzyDemo() {
        state.pose = "dizzy";
        state.emotion = "dizzy";
        state.stageCaption = "어지러워요! (세션 급전환)";
        state.stageQuip = "으아~ 세션을 너무 빠르게 바꿔서 핑글핑글 돌아요!";
        bumpTier1("세션 급전환 dizzy FX 프리뷰 활성화", 110);
        return clone({
          pose: state.pose,
          emotion: state.emotion,
          phoneFaceDown: state.phoneFaceDown,
          stageCaption: state.stageCaption,
          stageQuip: state.stageQuip,
          tier1: state.tier1
        });
      },

      async resetTier1Rotation() {
        state.tier1.turns = 0;
        state.tier1.tokens = 210;
        state.tier1.ttlRemainingSec = 300;
        state.tier1.summary = "회전 컨텍스트 리셋 완료 · Tier-2 최신 요약(1줄)만 가볍게 재주입됨";
        return clone(state.tier1);
      },

      async navigateView(viewId) {
        state.currentView = viewId === "companion-settings" ? "companion-settings" : "session";
        return { currentView: state.currentView };
      }
    };
  }

  global.WsCompanionMessengerMockApi = {
    SESSION_ACCENT_PALETTE,
    sessionAccentForKey,
    createMockApi
  };
})(window);
