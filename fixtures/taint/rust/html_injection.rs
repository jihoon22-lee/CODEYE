use actix_web::{web, HttpResponse};
use axum::extract::Query;
use axum::response::Html;

#[derive(serde::Deserialize)]
pub struct Greeting {
    name: String,
}

pub async fn hello(Query(g): Query<Greeting>) -> Html<String> {
    Html(format!("<h1>Hello {}</h1>", g.name)) // expect: codeye.rust.html-injection
}

pub async fn page(q: web::Query<Greeting>) -> HttpResponse {
    HttpResponse::Ok()
        .content_type("text/html; charset=utf-8")
        .body(format!("<p>{}</p>", q.name)) // expect: codeye.rust.html-injection
}
