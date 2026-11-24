use axum::extract::{Query, State};
use rusqlite::Connection;
use sqlx::PgPool;

#[derive(serde::Deserialize)]
pub struct Search {
    name: String,
}

pub async fn find(State(pool): State<PgPool>, Query(q): Query<Search>) -> String {
    let rows = sqlx::query("SELECT id FROM users WHERE name = $1")
        .bind(&q.name)
        .fetch_all(&pool)
        .await;
    format!("{}", rows.map(|r| r.len()).unwrap_or(0))
}

pub fn mark(conn: &Connection, Query(q): Query<Search>) -> usize {
    conn.execute("UPDATE users SET seen = 1 WHERE name = ?1", [&q.name]).unwrap_or(0)
}
