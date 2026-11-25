use axum::extract::{Path, Query};
use std::collections::HashMap;
use std::process::Command;

pub async fn ping(Query(params): Query<HashMap<String, String>>) -> String {
    let host = params.get("host").cloned().unwrap_or_default();
    // The host is one argument of a fixed program, not shell text.
    let out = Command::new("ping").args(["-c", "1", "--", &host]).output();
    format!("{:?}", out.map(|o| o.status))
}

pub async fn count(Path(n): Path<String>) -> String {
    let n = n.parse::<u32>().unwrap_or(1);
    let out = Command::new("sh").arg("-c").arg(format!("seq {}", n)).output();
    format!("{:?}", out.map(|o| o.status))
}
