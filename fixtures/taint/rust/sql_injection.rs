use axum::extract::{Query, State};
use rusqlite::Connection;
use sqlx::PgPool;

#[derive(serde::Deserialize)]
pub struct Search {
    name: String,
    sort: String,
}

pub async fn find(State(pool): State<PgPool>, Query(q): Query<Search>) -> String {
    let sql = format!("SELECT id FROM users WHERE name = '{}'", q.name);
    let rows = sqlx::query(&sql).fetch_all(&pool).await; // expect: codeye.rust.sql-injection
    format!("{}", rows.map(|r| r.len()).unwrap_or(0))
}

pub fn sorted(conn: &Connection, Query(q): Query<Search>) -> usize {
    conn.execute(&format!("UPDATE users SET seen = 1 ORDER BY {}", q.sort), []) // expect: codeye.rust.sql-injection
        .unwrap_or(0)
}
