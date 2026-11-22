// The strip commands are the only app commands. Declaring them makes each one
// require an explicit permission, which tauri.conf.json grants to the strip
// webview alone.
const STRIP_COMMANDS: &[&str] = &[
    "strip_popup",
    "strip_minimize",
    "strip_toggle_maximize",
    "strip_close",
    "strip_drag",
];

fn main() {
    let attributes = tauri_build::Attributes::new()
        .app_manifest(tauri_build::AppManifest::new().commands(STRIP_COMMANDS));
    if let Err(error) = tauri_build::try_build(attributes) {
        println!("{error:#}");
        std::process::exit(1);
    }
}
