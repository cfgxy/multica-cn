-- Workload weight (RUYI-397): per-agent multiplier on the claim budget. A
-- running task occupies resource_weight slots of max_concurrent_tasks, so a
-- heavy agent reaches the same ceiling sooner than a light one and one
-- runaway workload cannot starve every other agent sharing the machine.
-- 1 keeps the historical one-task-per-slot behaviour; the API enforces
-- 1..10 and treats an omitted field as 1.
ALTER TABLE agent
    ADD COLUMN resource_weight INT NOT NULL DEFAULT 1;
