#include <sqlite3.h>
#include <stdio.h>
#include <stdlib.h>

int main(void) {
    sqlite3 *db;
    char sql[512];
    if (sqlite3_open("app.db", &db) != SQLITE_OK) {
        return 1;
    }
    snprintf(sql, sizeof(sql), "SELECT name FROM users WHERE id = '%s'", getenv("QUERY_STRING"));
    sqlite3_exec(db, sql, NULL, NULL, NULL); /* expect: codeye.c.sql-injection */
    sqlite3_close(db);
    return 0;
}
