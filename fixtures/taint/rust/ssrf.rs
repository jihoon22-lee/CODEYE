use axum::extract::{Query, State};

#[derive(serde::Deserialize, serde::Serialize)]
pub struct Hook {
    url: String,
    host: String,
}

pub async fn fetch(Query(h): Query<Hook>) -> String {
    let body = reqwest::get(&h.url).await; // expect: codeye.rust.ssrf
    format!("{}", body.is_ok())
}

pub async fn notify(State(client): State<reqwest::Client>, Query(h): Query<Hook>) -> String {
    let target = format!("https://{}/hook", h.host);
    let r = client.post(&target).json(&h).send().await; // expect: codeye.rust.ssrf
    format!("{}", r.is_ok())
}
