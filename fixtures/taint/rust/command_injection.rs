use axum::extract::{Path, Query};
use std::collections::HashMap;
use std::process::Command;

pub async fn tool(Path(name): Path<String>) -> String {
    let out = Command::new(&name).output(); // expect: codeye.rust.command-injection
    format!("{:?}", out.map(|o| o.status))
}

fn run_shell(script: &str) -> std::io::Result<std::process::Output> {
    Command::new("sh").arg("-c").arg(script).output() // expect: codeye.rust.command-injection
}

pub async fn ping(Query(params): Query<HashMap<String, String>>) -> String {
    let host = params.get("host").cloned().unwrap_or_default();
    let _ = run_shell(&format!("ping -c 1 {}", host));
    String::new()
}
