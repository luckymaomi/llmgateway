const gatewayKeyOperationPrefix = 'llmgateway:pending-gateway-key:'
const configurationOperationPrefix = 'llmgateway:pending-configuration:'
const credentialOperationPrefix = 'llmgateway:pending-credential:'
const invitationOperationPrefix = 'llmgateway:pending-invitation:'
const entitlementOperationPrefix = 'llmgateway:pending-entitlement:'

function storageKey(userId: string): string {
  return gatewayKeyOperationPrefix + userId
}

function configurationStorageKey(userId: string): string {
  return configurationOperationPrefix + userId
}

function credentialStorageKey(userId: string): string {
  return credentialOperationPrefix + userId
}

function invitationStorageKey(userId: string): string {
  return invitationOperationPrefix + userId
}

function entitlementStorageKey(userId: string): string {
  return entitlementOperationPrefix + userId
}

function store(key: string, operation: unknown): boolean {
  try {
    window.sessionStorage.setItem(key, JSON.stringify(operation))
    return true
  } catch {
    return false
  }
}

function load(key: string): unknown {
  try {
    const encoded = window.sessionStorage.getItem(key)
    return encoded ? (JSON.parse(encoded) as unknown) : undefined
  } catch {
    return undefined
  }
}

function clear(key: string): void {
  try {
    window.sessionStorage.removeItem(key)
  } catch {
    // Storage can be unavailable in hardened browsers; there is no persisted operation to clear.
  }
}

export function storePendingGatewayKeyOperation(userId: string, operation: unknown): boolean {
  return store(storageKey(userId), operation)
}

export function loadPendingGatewayKeyOperation(userId: string): unknown {
  return load(storageKey(userId))
}

export function clearPendingGatewayKeyOperation(userId: string): void {
  clear(storageKey(userId))
}

export function storePendingConfigurationOperation(userId: string, operation: unknown): boolean {
  return store(configurationStorageKey(userId), operation)
}

export function loadPendingConfigurationOperation(userId: string): unknown {
  return load(configurationStorageKey(userId))
}

export function clearPendingConfigurationOperation(userId: string): void {
  clear(configurationStorageKey(userId))
}

export function storePendingCredentialOperation(userId: string, operation: unknown): boolean {
  return store(credentialStorageKey(userId), operation)
}

export function loadPendingCredentialOperation(userId: string): unknown {
  return load(credentialStorageKey(userId))
}

export function clearPendingCredentialOperation(userId: string): void {
  clear(credentialStorageKey(userId))
}

export function storePendingInvitationOperation(userId: string, operation: unknown): boolean {
  return store(invitationStorageKey(userId), operation)
}

export function loadPendingInvitationOperation(userId: string): unknown {
  return load(invitationStorageKey(userId))
}

export function clearPendingInvitationOperation(userId: string): void {
  clear(invitationStorageKey(userId))
}

export function storePendingEntitlementOperation(userId: string, operation: unknown): boolean {
  return store(entitlementStorageKey(userId), operation)
}

export function loadPendingEntitlementOperation(userId: string): unknown {
  return load(entitlementStorageKey(userId))
}

export function clearPendingEntitlementOperation(userId: string): void {
  clear(entitlementStorageKey(userId))
}

export function clearPendingBrowserOperations(): void {
  try {
    for (let index = window.sessionStorage.length - 1; index >= 0; index -= 1) {
      const key = window.sessionStorage.key(index)
      if (key?.startsWith('llmgateway:pending-')) window.sessionStorage.removeItem(key)
    }
  } catch {
    // Session state still clears even when the browser denies storage access.
  }
}
