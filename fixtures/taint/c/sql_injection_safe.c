#include <sqlite3.h>
#include <stdio.h>
#include <stdlib.h>

int main(void) {
    sqlite3 *db;
    sqlite3_stmt *stmt;
    char sql[128];
    if (sqlite3_open("app.db", &db) != SQLITE_OK) {
        return 1;
    }
    sqlite3_prepare_v2(db, "SELECT name FROM users WHERE id = ?", -1, &stmt, NULL);
    sqlite3_bind_text(stmt, 1, getenv("QUERY_STRING"), -1, SQLITE_TRANSIENT);
    sqlite3_step(stmt);
    sqlite3_finalize(stmt);
    snprintf(sql, sizeof(sql), "SELECT name FROM users LIMIT %ld", strtol(getenv("QUERY_STRING"), NULL, 10));
    sqlite3_exec(db, sql, NULL, NULL, NULL);
    sqlite3_close(db);
    return 0;
}
