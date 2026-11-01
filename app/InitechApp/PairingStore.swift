import Foundation
import Security

/// PairingStore keeps the one pairing the phone holds. The token is the whole
/// gate to every pane, so it lives in the keychain, readable only on this
/// device and only while it is unlocked.
protocol PairingStore: Sendable {
    func load() -> Pairing?
    func save(_ pairing: Pairing) throws
    func clear() throws
}

/// KeychainError carries the Security framework status a keychain call failed with.
struct KeychainError: Error, Equatable {
    let status: OSStatus
}

/// KeychainPairingStore is the PairingStore the app runs on.
struct KeychainPairingStore: PairingStore {
    /// service names the keychain item; tests pass their own so they never
    /// touch the app's real pairing.
    var service = "co.nmelo.initech.pairing"
    private let account = "device"

    private var query: [String: Any] {
        [kSecClass as String: kSecClassGenericPassword,
         kSecAttrService as String: service,
         kSecAttrAccount as String: account]
    }

    func load() -> Pairing? {
        var q = query
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: AnyObject?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess,
              let data = out as? Data else { return nil }
        return try? JSONDecoder().decode(Pairing.self, from: data)
    }

    func save(_ pairing: Pairing) throws {
        let data = try JSONEncoder().encode(pairing)
        try clear()
        var q = query
        q[kSecValueData as String] = data
        q[kSecAttrAccessible as String] = kSecAttrAccessibleWhenUnlockedThisDeviceOnly
        let status = SecItemAdd(q as CFDictionary, nil)
        guard status == errSecSuccess else { throw KeychainError(status: status) }
    }

    func clear() throws {
        let status = SecItemDelete(query as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw KeychainError(status: status)
        }
    }
}
