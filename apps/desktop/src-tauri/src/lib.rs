#[derive(serde::Serialize)]
#[serde(rename_all = "camelCase")]
struct EnvironmentInfo {
    desktop_shell: &'static str,
    agent_runtime: &'static str,
    user_interface: &'static str,
}

#[tauri::command]
fn environment_info() -> EnvironmentInfo {
    EnvironmentInfo {
        desktop_shell: "Tauri 2",
        agent_runtime: "Node.js + TypeScript",
        user_interface: "React + TypeScript",
    }
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .invoke_handler(tauri::generate_handler![environment_info])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
