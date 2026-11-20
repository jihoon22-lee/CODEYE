ALTER TABLE users
    ADD COLUMN email text COLLATE "C" NULL,
    ADD CONSTRAINT users_email_unique UNIQUE (email),
    ADD CONSTRAINT users_email_normalized CHECK (
        email IS NULL OR (octet_length(email) <= 254 AND position('@' IN email) <= 65 AND
        email ~ '^[a-z0-9!#$%&''*+/=?^_`{|}~-]+(\.[a-z0-9!#$%&''*+/=?^_`{|}~-]+)*@[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$')),
    DROP CONSTRAINT users_password_needs_username,
    DROP CONSTRAINT users_must_change_needs_username,
    ADD CONSTRAINT users_password_needs_identifier CHECK (password_kdf IS NULL OR username IS NOT NULL OR email IS NOT NULL),
    ADD CONSTRAINT users_must_change_needs_identifier CHECK (NOT must_change_password OR username IS NOT NULL OR email IS NOT NULL);

CREATE OR REPLACE FUNCTION codeye_users_account_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.username IS NOT NULL AND NEW.username IS DISTINCT FROM OLD.username THEN
        RAISE EXCEPTION 'username is immutable once set' USING ERRCODE = 'CY001';
    END IF;
    IF OLD.email IS NOT NULL AND NEW.email IS DISTINCT FROM OLD.email THEN
        RAISE EXCEPTION 'email is immutable once set' USING ERRCODE = 'CY001';
    END IF;
    RETURN NEW;
END;
$$;
