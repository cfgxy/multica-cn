export const CREDENTIAL_ENVELOPE_VERSION = 1 as const;
export const CREDENTIAL_ENVELOPE_ALGORITHM = "RSA-OAEP-256+A256GCM" as const;

export interface CredentialAAD {
  workspace_id: string;
  resource_type: string;
  resource_id: string;
  subject_type: string;
  subject_id: string;
  purpose: string;
  version: typeof CREDENTIAL_ENVELOPE_VERSION;
}

export interface CredentialTransportPublicKey {
  key_id: string;
  algorithm: typeof CREDENTIAL_ENVELOPE_ALGORITHM;
  public_key_spki: string;
}

export interface CredentialTransportEnvelope {
  version: typeof CREDENTIAL_ENVELOPE_VERSION;
  key_id: string;
  algorithm: typeof CREDENTIAL_ENVELOPE_ALGORITHM;
  wrapped_data_key: string;
  nonce: string;
  ciphertext: string;
}

export async function encryptCredentialForTransport(
  plaintext: string,
  publicKey: CredentialTransportPublicKey,
  context: CredentialAAD,
  cryptoProvider: Crypto = globalThis.crypto,
): Promise<CredentialTransportEnvelope> {
  if (
    !plaintext ||
    !publicKey.key_id ||
    publicKey.algorithm !== CREDENTIAL_ENVELOPE_ALGORITHM ||
    context.version !== CREDENTIAL_ENVELOPE_VERSION ||
    !context.workspace_id ||
    !context.resource_type ||
    !context.resource_id ||
    !context.subject_type ||
    !context.subject_id ||
    !context.purpose
  ) {
    throw new Error("credential encryption unavailable");
  }

  const aad = new TextEncoder().encode(JSON.stringify(context));
  const label = await cryptoProvider.subtle.digest("SHA-256", aad);
  const importedPublicKey = await cryptoProvider.subtle.importKey(
    "spki",
    decodeBase64(publicKey.public_key_spki),
    { name: "RSA-OAEP", hash: "SHA-256" },
    false,
    ["encrypt"],
  );

  const dataKey = cryptoProvider.getRandomValues(new Uint8Array(32));
  const nonce = cryptoProvider.getRandomValues(new Uint8Array(12));
  let plaintextBytes: Uint8Array<ArrayBuffer> | undefined;
  try {
    const aesKey = await cryptoProvider.subtle.importKey("raw", dataKey, "AES-GCM", false, ["encrypt"]);
    plaintextBytes = new TextEncoder().encode(plaintext);
    const ciphertext = await cryptoProvider.subtle.encrypt(
      { name: "AES-GCM", iv: nonce, additionalData: aad, tagLength: 128 },
      aesKey,
      plaintextBytes,
    );
    const wrappedKey = await cryptoProvider.subtle.encrypt(
      { name: "RSA-OAEP", label },
      importedPublicKey,
      dataKey,
    );
    return {
      version: CREDENTIAL_ENVELOPE_VERSION,
      key_id: publicKey.key_id,
      algorithm: CREDENTIAL_ENVELOPE_ALGORITHM,
      wrapped_data_key: encodeBase64(new Uint8Array(wrappedKey)),
      nonce: encodeBase64(nonce),
      ciphertext: encodeBase64(new Uint8Array(ciphertext)),
    };
  } catch {
    throw new Error("credential encryption unavailable");
  } finally {
    dataKey.fill(0);
    plaintextBytes?.fill(0);
  }
}

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
