use actix_web::{web, HttpResponse};
use axum::extract::Query;
use axum::response::Html;

#[derive(serde::Deserialize)]
pub struct Greeting {
    name: String,
}

pub async fn hello(Query(g): Query<Greeting>) -> Html<String> {
    Html(format!("<h1>Hello {}</h1>", html_escape::encode_text(&g.name)))
}

pub async fn page(q: web::Query<Greeting>) -> HttpResponse {
    HttpResponse::Ok()
        .content_type("text/plain; charset=utf-8")
        .body(format!("hello {}", q.name))
}
