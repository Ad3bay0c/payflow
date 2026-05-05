BEGIN;

    DROP TABLE IF EXISTS auth_audit_log;
    DROP TABLE IF EXISTS user_credentials;
    DROP TABLE IF EXISTS users;
    DROP EXTENSION IF EXISTS "uuid-ossp";

COMMIT;
