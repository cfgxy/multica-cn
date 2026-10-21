// @vitest-environment node
import { webcrypto } from "node:crypto";
import { describe, expect, it } from "vitest";
import {
  CREDENTIAL_ENVELOPE_ALGORITHM,
  CREDENTIAL_ENVELOPE_VERSION,
  encryptCredentialForTransport,
  type CredentialAAD,
} from "./envelope";

const cryptoProvider = webcrypto as unknown as Crypto;

const context: CredentialAAD = {
  workspace_id: "workspace-fixture",
  resource_type: "issue",
  resource_id: "issue-fixture",
  subject_type: "agent",
  subject_id: "agent-fixture",
  purpose: "credential.create",
  version: CREDENTIAL_ENVELOPE_VERSION,
};

describe("credential transport envelope", () => {
  it("encrypts independently of HTTP and binds ciphertext and wrapped key to AAD", async () => {
    const pair = await cryptoProvider.subtle.generateKey(
      { name: "RSA-OAEP", modulusLength: 2048, publicExponent: new Uint8Array([1, 0, 1]), hash: "SHA-256" },
      true,
      ["encrypt", "decrypt"],
    );
    const spki = new Uint8Array(await cryptoProvider.subtle.exportKey("spki", pair.publicKey));
    const publicKey = {
      key_id: "transport-v1",
      algorithm: CREDENTIAL_ENVELOPE_ALGORITHM,
      public_key_spki: encodeBase64(spki),
    } as const;

    const first = await encryptCredentialForTransport("synthetic-fixture", publicKey, context, cryptoProvider);
    const second = await encryptCredentialForTransport("synthetic-fixture", publicKey, context, cryptoProvider);
    expect(first).not.toEqual(second);
    expect(JSON.stringify(first)).not.toContain("synthetic-fixture");
    expect(first).toMatchObject({ version: 1, key_id: "transport-v1", algorithm: CREDENTIAL_ENVELOPE_ALGORITHM });

    const aad = new TextEncoder().encode(JSON.stringify(context));
    const label = await cryptoProvider.subtle.digest("SHA-256", aad);
    const dataKey = await cryptoProvider.subtle.decrypt(
      { name: "RSA-OAEP", label },
      pair.privateKey,
      decodeBase64(first.wrapped_data_key),
    );
    const aesKey = await cryptoProvider.subtle.importKey("raw", dataKey, "AES-GCM", false, ["decrypt"]);
    const plaintext = await cryptoProvider.subtle.decrypt(
      { name: "AES-GCM", iv: decodeBase64(first.nonce), additionalData: aad, tagLength: 128 },
      aesKey,
      decodeBase64(first.ciphertext),
    );
    expect(new TextDecoder().decode(plaintext)).toBe("synthetic-fixture");

    const changedContext = { ...context, resource_id: "other-issue" };
    const changedAAD = new TextEncoder().encode(JSON.stringify(changedContext));
    const changedLabel = await cryptoProvider.subtle.digest("SHA-256", changedAAD);
    await expect(
      cryptoProvider.subtle.decrypt(
        { name: "RSA-OAEP", label: changedLabel },
        pair.privateKey,
        decodeBase64(first.wrapped_data_key),
      ),
    ).rejects.toThrow();
  });

  it("fails closed on missing or mismatched protocol metadata", async () => {
    const publicKey = {
      key_id: "transport-v1",
      algorithm: "wrong-algorithm",
      public_key_spki: "unused",
    } as never;
    await expect(encryptCredentialForTransport("synthetic-fixture", publicKey, context, cryptoProvider)).rejects.toThrow(
      "credential encryption unavailable",
    );
    await expect(
      encryptCredentialForTransport("synthetic-fixture", {
        key_id: "transport-v1",
        algorithm: CREDENTIAL_ENVELOPE_ALGORITHM,
        public_key_spki: "unused",
      }, { ...context, version: 2 as 1 }, cryptoProvider),
    ).rejects.toThrow("credential encryption unavailable");
  });
});

function encodeBase64(value: Uint8Array): string {
  let binary = "";
  for (const byte of value) binary += String.fromCharCode(byte);
  return btoa(binary).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/, "");
}

function decodeBase64(value: string): ArrayBuffer {
  const base64 = value.replaceAll("-", "+").replaceAll("_", "/");
  const binary = atob(base64 + "=".repeat((4 - (base64.length % 4)) % 4));
  return Uint8Array.from(binary, (character) => character.charCodeAt(0)).buffer;
}
