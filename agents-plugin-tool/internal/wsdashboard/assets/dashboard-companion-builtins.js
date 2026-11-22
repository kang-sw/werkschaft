// Built-in companion scenarios and effect badges: the one source the shell
// bundle and the daemon's agent director both read (companion_director.go
// parses the JSON after the assignment). Keep the text after "= " strict JSON.
window.WsCompanionBuiltins = {
  "scenarios": {
    "idle_roam": {
      "on": [
        "session.idle",
        "idle.long"
      ],
      "priority": 5,
      "cooldownMs": 5000,
      "durationMs": 1250,
      "pose": "stand",
      "expression": "idle",
      "gaze": "cursor",
      "stage": "hop-bounce"
    },
    "working_tatata": {
      "on": "session.replying",
      "priority": 10,
      "durationMs": 1800,
      "pose": "typing",
      "expression": "focused",
      "gaze": "scan"
    },
    "attention_point": {
      "on": "ask.open",
      "priority": 65,
      "cooldownMs": 900,
      "durationMs": 1700,
      "pose": "stand",
      "gesture": "point",
      "expression": "oo",
      "gaze": "whiteboard-ask",
      "stage": "hop-bounce",
      "fx": "alert"
    },
    "startled_poke": {
      "on": "user.poke",
      "priority": 45,
      "durationMs": 550,
      "pose": "stand",
      "expression": "oo",
      "stage": "squash-pop",
      "fx": "shock"
    },
    "drag_dangle": {
      "on": "user.drag",
      "priority": 100,
      "durationMs": 0,
      "pose": "dangle",
      "expression": "oo",
      "stage": "pendulum-drag"
    },
    "gloom_fail": {
      "on": "session.error",
      "priority": 70,
      "cooldownMs": 1000,
      "durationMs": 2100,
      "pose": "stand",
      "gesture": "headrub",
      "expression": "cry",
      "gaze": "down"
    },
    "celebrate_done": {
      "on": "session.celebrate",
      "priority": 60,
      "cooldownMs": 1000,
      "durationMs": 1400,
      "pose": "stand",
      "gesture": "wave",
      "expression": "happy",
      "stage": "double-hop",
      "fx": "sparkle"
    },
    "subagent_dispatch": {
      "on": "subagent.spawn",
      "priority": 55,
      "cooldownMs": 500,
      "durationMs": 2400,
      "pose": "stand",
      "gesture": "cup_hands",
      "expression": "oo",
      "stage": "anticipate-launch",
      "gaze": "prop:drone",
      "prop": {
        "id": "drone",
        "motion": "hand-liftoff",
        "action": "launch",
        "from": "hand",
        "to": "hud-roster"
      }
    },
    "subagent_return": {
      "on": "subagent.done",
      "priority": 56,
      "cooldownMs": 500,
      "steps": [
        {
          "ms": 2200,
          "pose": "stand",
          "gesture": "cup_hands",
          "expression": "oo",
          "gaze": "prop:drone",
          "prop": {
            "id": "drone",
            "motion": "hand-liftoff",
            "action": "return",
            "from": "hud-roster",
            "to": "hand"
          }
        },
        {
          "ms": 280,
          "pose": "stand",
          "gesture": "cup_hands",
          "expression": "happy",
          "stage": "settle-sigh"
        }
      ]
    },
    "mail_send": {
      "on": "mail.send",
      "priority": 54,
      "durationMs": 2000,
      "pose": "stand",
      "gesture": "cup_hands",
      "expression": "oo",
      "stage": "anticipate-launch",
      "gaze": "prop:mail",
      "prop": {
        "id": "mail",
        "motion": "hand-liftoff",
        "from": "hand",
        "to": "hud-roster"
      }
    },
    "mailbox_bonk": {
      "on": "mail.recv",
      "priority": 57,
      "durationMs": 2000,
      "pose": "stand",
      "expression": "oo",
      "gaze": "prop:mail",
      "prop": {
        "id": "mail",
        "motion": "bonk-from-edge"
      }
    },
    "global_session_done": {
      "on": "global.session_done",
      "priority": 52,
      "durationMs": 1600,
      "pose": "stand",
      "gesture": "wave",
      "expression": "happy",
      "gaze": "rail-alert"
    },
    "session_compacted": {
      "on": "session.compacted",
      "priority": 48,
      "durationMs": 1600,
      "pose": "stand",
      "expression": "half_lid",
      "stage": "settle-sigh"
    },
    "route_switch": {
      "on": "route.switch",
      "priority": 25,
      "durationMs": 900,
      "pose": "stand",
      "expression": "idle",
      "gaze": "cursor",
      "stage": "hop-bounce"
    }
  },
  "fx": {
    "alert": {
      "kind": "badge",
      "text": "!",
      "color": "#e2582c"
    },
    "ponder": {
      "kind": "float",
      "text": "…",
      "color": "#8fa0b8"
    },
    "shock": {
      "kind": "badge",
      "text": "!?",
      "color": "#e2582c"
    },
    "sparkle": {
      "kind": "particles",
      "glyphs": [
        "✦",
        "✧",
        "✦"
      ],
      "color": "#e6aa34"
    },
    "zzz": {
      "kind": "float",
      "text": "Zzz",
      "color": "#8fa0b8"
    }
  }
};
