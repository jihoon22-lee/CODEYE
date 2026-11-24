use axum::extract::{Query, State};

#[derive(serde::Deserialize, serde::Serialize)]
pub struct Lookup {
    id: String,
}

pub async fn user(Query(q): Query<Lookup>) -> String {
    let body = reqwest::get(format!("https://api.example.com/users/{}", q.id)).await;
    format!("{}", body.is_ok())
}

pub async fn forward(State(client): State<reqwest::Client>, Query(q): Query<Lookup>) -> String {
    let r = client.post("https://api.example.com/events").json(&q).send().await;
    format!("{}", r.is_ok())
}
