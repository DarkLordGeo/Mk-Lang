/* sqlite-insert-select, C: the same amalgamation (sqlite3.c from
   $MK_EVIDENCE/vendor/sqlite, never in the repository) built with its
   own OS layer (os_win.c) and the C runtime's malloc, with the Mk row's
   other flags: SQLITE_THREADSAFE=0, SQLITE_OMIT_LOCALTIME,
   SQLITE_DEFAULT_MEMSTATUS=0, SQLITE_TEMP_STORE=3. n rows inserted in one
   transaction through one prepared statement, then n point selects by
   primary key, id = (k * 7919) mod n + 1. Usage: sqlite-insert-select <n>.
   Prints the sum of each selected id and its text's length (README.md). */
#include <stdio.h>
#include <stdlib.h>
#include "sqlite3.h"

int main(int argc, char **argv) {
    long long n = argc > 1 ? atoll(argv[1]) : 100000;
    sqlite3 *db;
    sqlite3_stmt *ins, *sel;
    long long sum = 0;
    if (sqlite3_open_v2(":memory:", &db, SQLITE_OPEN_READWRITE | SQLITE_OPEN_CREATE, NULL) != SQLITE_OK) return 1;
    if (sqlite3_exec(db, "create table t(id integer primary key, v text not null)", NULL, NULL, NULL) != SQLITE_OK) return 1;
    if (sqlite3_exec(db, "begin", NULL, NULL, NULL) != SQLITE_OK) return 1;
    if (sqlite3_prepare_v2(db, "insert into t(id, v) values(?, ?)", -1, &ins, NULL) != SQLITE_OK) return 1;
    for (long long i = 1; i <= n; i++) {
        sqlite3_bind_int64(ins, 1, i);
        sqlite3_bind_text(ins, 2, "abcdefghijklmnop", 16, SQLITE_TRANSIENT);
        if (sqlite3_step(ins) != SQLITE_DONE) return 2;
        sqlite3_reset(ins);
    }
    sqlite3_finalize(ins);
    if (sqlite3_exec(db, "commit", NULL, NULL, NULL) != SQLITE_OK) return 1;
    if (sqlite3_prepare_v2(db, "select v, id from t where id = ?", -1, &sel, NULL) != SQLITE_OK) return 1;
    for (long long k = 0; k < n; k++) {
        sqlite3_bind_int64(sel, 1, (k * 7919) % n + 1);
        if (sqlite3_step(sel) != SQLITE_ROW) return 3;
        const unsigned char *v = sqlite3_column_text(sel, 0);
        int len = sqlite3_column_bytes(sel, 0);
        sum += (v ? len : 0) + sqlite3_column_int64(sel, 1);
        sqlite3_reset(sel);
    }
    sqlite3_finalize(sel);
    sqlite3_close_v2(db);
    printf("%lld\n", sum);
    return 0;
}
