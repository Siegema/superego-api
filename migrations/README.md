# Database migrations

Migrations are versioned SQL files applied explicitly with `psql`. No migration
framework or automatic API startup migration is used for this first slice.
The PostgreSQL container image in `build/` bootstraps `0001_entries.up.sql` only
when initializing an empty data volume. Do not reapply it to that database;
existing volumes and later migrations still require explicit migration steps.

`0001_entries.up.sql` creates minimal local users, private entries, the ownership
foreign key, and the owner/pagination index. It does not seed a development user;
the local server creates that identity at startup.

Apply once to an empty, dedicated Superego database:

```sh
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/0001_entries.up.sql
```

Record applied versions operationally; these scripts do not maintain a migration
ledger and are intentionally not idempotent. Stop the API before rolling back.
**Rollback permanently deletes all entries and users:**

```sh
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/0001_entries.down.sql
```

Both directions use transactions. Back up real data before schema changes. Add
new numbered migrations for future changes instead of modifying applied files.
