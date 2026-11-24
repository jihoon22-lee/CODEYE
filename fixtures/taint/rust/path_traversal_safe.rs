use actix_web::{web, HttpResponse};
use std::fs;
use std::path::{Path, PathBuf};

pub async fn read(path: web::Path<String>) -> HttpResponse {
    let name = path.into_inner();
    let leaf = Path::new(&name).file_name().unwrap_or_default().to_owned();
    match fs::read_to_string(PathBuf::from("/srv/files").join(leaf)) {
        Ok(body) => HttpResponse::Ok().body(body),
        Err(_) => HttpResponse::NotFound().finish(),
    }
}

pub async fn download(path: web::Path<String>) -> HttpResponse {
    let root = PathBuf::from("/srv/files");
    let full = match root.join(path.into_inner()).canonicalize() {
        Ok(p) => p,
        Err(_) => return HttpResponse::NotFound().finish(),
    };
    if !full.starts_with(&root) {
        return HttpResponse::BadRequest().finish();
    }
    match fs::read(&full) {
        Ok(body) => HttpResponse::Ok().body(body),
        Err(_) => HttpResponse::NotFound().finish(),
    }
}
