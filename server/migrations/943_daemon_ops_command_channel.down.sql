-- Down for 943_daemon_ops_command_channel (RUYI-645). Both tables are
-- self-contained evidence stores introduced by this migration; no other
-- schema object references them (the audit_event rows written alongside
-- commands are runtime evidence by design and stay).
DROP TABLE IF EXISTS daemon_service_inventory;
DROP TABLE IF EXISTS daemon_ops_command;
