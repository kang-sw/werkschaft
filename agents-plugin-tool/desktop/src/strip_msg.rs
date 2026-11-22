// The fixed formats between the shell, the dashboard and the title strip page.

use serde_json::{json, Value};

/// The dashboard's report of its resolved theme: `werkschaft:theme:light` or
/// `werkschaft:theme:dark`, posted through the page's WebView2 message channel.
pub const THEME_PREFIX: &str = "werkschaft:theme:";
/// The longest theme message the shell looks at, in bytes.
pub const THEME_MESSAGE_MAX: usize = 32;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Theme {
    Light,
    Dark,
}

impl Theme {
    fn name(self) -> &'static str {
        match self {
            Theme::Light => "light",
            Theme::Dark => "dark",
        }
    }
}

pub fn parse_theme(message: &str) -> Option<Theme> {
    if message.len() > THEME_MESSAGE_MAX {
        return None;
    }
    match message.strip_prefix(THEME_PREFIX)? {
        "light" => Some(Theme::Light),
        "dark" => Some(Theme::Dark),
        _ => None,
    }
}

/// What the strip page shows. `theme` is None until the dashboard reports, and
/// the page then follows the OS preference.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct State {
    pub maximized: bool,
    pub focused: bool,
    pub title: String,
    pub theme: Option<Theme>,
}

impl Default for State {
    fn default() -> Self {
        Self {
            maximized: false,
            focused: true,
            title: "Werkschaft".to_owned(),
            theme: None,
        }
    }
}

impl State {
    pub fn json(&self) -> Value {
        json!({
            "maximized": self.maximized,
            "focused": self.focused,
            "title": self.title,
            "theme": self.theme.map(Theme::name),
        })
    }
}

/// The script that hands the strip page a state patch.
pub fn set_script(patch: &Value) -> String {
    format!("window.werkschaftStrip && window.werkschaftStrip.set({patch});")
}

/// The script that opens a menu from the keyboard.
pub fn open_script(menu: &str) -> String {
    format!(
        "window.werkschaftStrip && window.werkschaftStrip.openMenu({});",
        json!(menu)
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn theme_messages_are_exactly_light_or_dark() {
        assert_eq!(parse_theme("werkschaft:theme:light"), Some(Theme::Light));
        assert_eq!(parse_theme("werkschaft:theme:dark"), Some(Theme::Dark));
        for bad in [
            "werkschaft:theme:",
            "werkschaft:theme:Dark",
            "werkschaft:theme:dark ",
            "werkschaft:theme:auto",
            "werkschaft:theme:{\"theme\":\"dark\"}",
            "werkschaft:notify:dark",
            "dark",
            "",
        ] {
            assert_eq!(parse_theme(bad), None, "{bad:?}");
        }
        let long = format!("{THEME_PREFIX}{}", "d".repeat(THEME_MESSAGE_MAX));
        assert_eq!(parse_theme(&long), None);
    }

    #[test]
    fn state_json_carries_the_fields_the_page_reads() {
        let mut state = State::default();
        assert_eq!(
            state.json(),
            json!({"maximized": false, "focused": true, "title": "Werkschaft", "theme": null})
        );
        state.maximized = true;
        state.title = "Werkschaft — \"x\"".into();
        state.theme = Some(Theme::Dark);
        assert_eq!(
            state.json(),
            json!({"maximized": true, "focused": true, "title": "Werkschaft — \"x\"", "theme": "dark"})
        );
    }

    #[test]
    fn scripts_embed_json_only() {
        assert_eq!(
            open_script("Con\"nection"),
            "window.werkschaftStrip && window.werkschaftStrip.openMenu(\"Con\\\"nection\");"
        );
        assert!(set_script(&json!({"maximized": true})).ends_with("set({\"maximized\":true});"));
    }
}
