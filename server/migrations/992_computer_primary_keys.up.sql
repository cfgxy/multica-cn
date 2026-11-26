ALTER TABLE computer ADD CONSTRAINT computer_pkey PRIMARY KEY USING INDEX computer_pkey_uidx;
ALTER TABLE computer_credential ADD CONSTRAINT computer_credential_pkey PRIMARY KEY USING INDEX computer_credential_pkey_uidx;
ALTER TABLE computer_binding ADD CONSTRAINT computer_binding_pkey PRIMARY KEY USING INDEX computer_binding_pkey_uidx;
ALTER TABLE computer_audit ADD CONSTRAINT computer_audit_pkey PRIMARY KEY USING INDEX computer_audit_pkey_uidx;
