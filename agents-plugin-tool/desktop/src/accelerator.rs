#[derive(Clone, Copy)]
pub enum Key {
    R,
    I,
    F12,
    Zero,
    Plus,
    Minus,
    M,
}

pub fn menu_id(key: Key, ctrl: bool, shift: bool) -> Option<&'static str> {
    match (key, ctrl, shift) {
        (Key::R, true, false) => Some("retry"),
        (Key::I, true, true) => Some("devtools"),
        (Key::F12, false, false) => Some("devtools_f12"),
        (Key::Zero, true, false) => Some("zoom_reset"),
        (Key::Plus, true, false) => Some("zoom_in"),
        (Key::Plus, true, true) => Some("zoom_in_shifted"),
        (Key::Minus, true, false) => Some("zoom_out"),
        // The Window menu's Minimize carries Ctrl+M, which a focused webview swallows.
        (Key::M, true, false) => Some("minimize"),
        _ => None,
    }
}

/// The menu labels, in strip order. F10 and a lone Alt open the first.
pub const MENUS: [&str; 6] = ["File", "Edit", "Window", "Help", "Connection", "View"];

/// The menu Alt plus `letter` opens: each menu's first letter, all distinct.
pub fn mnemonic_menu(letter: char) -> Option<&'static str> {
    let letter = letter.to_ascii_uppercase();
    MENUS.into_iter().find(|menu| menu.starts_with(letter))
}

/// Opens the File menu when Alt is pressed and released with no other key
/// between, as a native menu bar does. Any other key, or the mouse, cancels.
#[derive(Default)]
pub struct AltTap {
    pending: bool,
}

impl AltTap {
    pub fn alt_down(&mut self, repeat: bool) {
        if !repeat {
            self.pending = true;
        }
    }

    pub fn other_key(&mut self) {
        self.pending = false;
    }

    /// True when this release completes a lone tap.
    pub fn alt_up(&mut self) -> bool {
        std::mem::take(&mut self.pending)
    }
}

#[cfg(test)]
mod tests {
    use super::{menu_id, mnemonic_menu, AltTap, Key, MENUS};
    use crate::zoom::Step;

    #[test]
    fn focused_webview_keys_map_to_retry_and_devtools_menu_items() {
        assert_eq!(menu_id(Key::R, true, false), Some("retry"));
        assert_eq!(menu_id(Key::I, true, true), Some("devtools"));
        assert_eq!(menu_id(Key::F12, false, false), Some("devtools_f12"));
    }

    #[test]
    fn focused_webview_ctrl_m_minimizes() {
        assert_eq!(menu_id(Key::M, true, false), Some("minimize"));
        assert_eq!(menu_id(Key::M, true, true), None);
        assert_eq!(menu_id(Key::M, false, false), None);
    }

    #[test]
    fn focused_webview_keys_reuse_menu_zoom_steps() {
        for (key, shift, step) in [
            (Key::Zero, false, Step::Reset),
            (Key::Plus, false, Step::In),
            (Key::Plus, true, Step::In),
            (Key::Minus, false, Step::Out),
        ] {
            assert_eq!(
                menu_id(key, true, shift).and_then(Step::from_menu_id),
                Some(step)
            );
        }
    }

    #[test]
    fn typing_and_unassigned_modifier_combinations_have_no_action() {
        for key in [Key::R, Key::I, Key::Zero, Key::Plus, Key::Minus, Key::M] {
            assert_eq!(menu_id(key, false, false), None);
        }
        assert_eq!(menu_id(Key::R, true, true), None);
        assert_eq!(menu_id(Key::I, true, false), None);
        assert_eq!(menu_id(Key::F12, true, false), None);
        assert_eq!(menu_id(Key::Minus, true, true), None);
    }

    #[test]
    fn alt_letters_open_the_menu_with_that_first_letter() {
        for menu in MENUS {
            let letter = menu.chars().next().unwrap();
            assert_eq!(mnemonic_menu(letter), Some(menu));
            assert_eq!(mnemonic_menu(letter.to_ascii_lowercase()), Some(menu));
        }
        // Alt+F4 is the system's, and no menu starts with a digit or Z.
        assert_eq!(mnemonic_menu('4'), None);
        assert_eq!(mnemonic_menu('Z'), None);
    }

    #[test]
    fn a_lone_alt_tap_opens_a_menu_but_alt_with_another_key_does_not() {
        let mut tap = AltTap::default();
        tap.alt_down(false);
        assert!(tap.alt_up());
        assert!(!tap.alt_up());

        tap.alt_down(false);
        tap.other_key();
        assert!(!tap.alt_up());

        // Auto-repeat of a held Alt after another key does not re-arm it.
        tap.alt_down(false);
        tap.other_key();
        tap.alt_down(true);
        assert!(!tap.alt_up());
    }
}
