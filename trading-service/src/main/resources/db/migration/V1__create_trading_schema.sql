-- Flyway creates the history schema before running versioned migrations.
-- Keep the initial migration compatible with an existing empty schema.
CREATE SCHEMA IF NOT EXISTS trading;
