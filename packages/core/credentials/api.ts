import { z } from "zod";
import { CREDENTIAL_ENVELOPE_ALGORITHM } from "./envelope";

export const CredentialTransportKeySchema = z.object({
  key_id: z.string().min(1),
  algorithm: z.literal(CREDENTIAL_ENVELOPE_ALGORITHM),
  public_key_spki: z.string().min(1),
});

export const CredentialConnectorSchema = z.object({
  id: z.string(),
  name: z.string(),
  kind: z.string(),
  target_url: z.string(),
  adapter_policy: z.string(),
  auth_field: z.string(),
  status: z.string(),
  revision: z.number(),
});

export const CredentialMetadataSchema = z.object({
  id: z.string(),
  name: z.string(),
  target_service: z.string(),
  connector_id: z.string(),
  connector_revision: z.number(),
  purpose: z.string(),
  status: z.string(),
  resource_scope: z.string(),
  subject_type: z.string(),
  subject_id: z.string(),
  expires_at: z.string().nullable().optional(),
  created_at: z.string(),
});

export type CredentialTransportKey = {
  key_id: string;
  algorithm: typeof CREDENTIAL_ENVELOPE_ALGORITHM;
  public_key_spki: string;
};
export type CredentialConnector = z.infer<typeof CredentialConnectorSchema>;
export type CredentialMetadata = z.infer<typeof CredentialMetadataSchema>;

export const CredentialConnectorListSchema = z.array(CredentialConnectorSchema);
export const CredentialMetadataListSchema = z.array(CredentialMetadataSchema);
