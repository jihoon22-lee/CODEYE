use actix_web::{web, HttpResponse};
use std::fs;
use std::path::PathBuf;

pub async fn read(path: web::Path<String>) -> HttpResponse {
    let full = PathBuf::from("/srv/files").join(path.into_inner());
    match fs::read_to_string(&full) { // expect: codeye.rust.path-traversal
        Ok(body) => HttpResponse::Ok().body(body),
        Err(_) => HttpResponse::NotFound().finish(),
    }
}

pub async fn remove(web::Query(q): web::Query<Target>) -> HttpResponse {
    let _ = tokio::fs::remove_file(format!("/srv/files/{}", q.name)).await; // expect: codeye.rust.path-traversal
    HttpResponse::Ok().finish()
}

#[derive(serde::Deserialize)]
pub struct Target {
    name: String,
}
