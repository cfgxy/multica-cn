-- Backing index for runtime_cost_budget's primary key, attached in 965 via
-- PRIMARY KEY USING INDEX. Own single-statement migration so CONCURRENTLY runs
-- outside an implicit transaction (repo convention, migrations 332-334).
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS runtime_cost_budget_pkey_uidx
    ON runtime_cost_budget (id);
