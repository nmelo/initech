import CryptoKit
import Foundation

/// Pairing is what the phone needs to reach the Mac service: its address, the
/// device token `initech app-serve token` issued, and the fingerprint of the
/// service's self-signed certificate, the only certificate the app accepts.
/// The Mac shows it once, as a QR code and as text; both carry the same URL.
struct Pairing: Equatable, Codable, Sendable {
    var host: String
    var port: Int
    var token: String
    /// fingerprint is the base64url SHA-256 of the service certificate's DER.
    var fingerprint: String

    /// The service base URL the client sends every request to.
    var baseURL: URL {
        var c = URLComponents()
        c.scheme = "https"
        c.host = host
        c.port = port
        return c.url!
    }
}

/// PairingError names why a scanned or pasted code was not a pairing.
enum PairingError: Error, Equatable {
    case notAPairingCode
    case missing(String)
    case badPort
}

extension Pairing {
    /// parse reads a pairing URL, `initech://pair?host=<h>&port=<n>&token=<t>&fp=<f>`.
    /// Surrounding whitespace is ignored (a pasted line carries its newline);
    /// anything else is refused, so a wrong QR never becomes a stored token.
    static func parse(_ text: String) throws -> Pairing {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let c = URLComponents(string: trimmed),
              c.scheme == "initech", c.host == "pair" else {
            throw PairingError.notAPairingCode
        }
        func value(_ name: String) throws -> String {
            guard let v = c.queryItems?.first(where: { $0.name == name })?.value,
                  !v.isEmpty else {
                throw PairingError.missing(name)
            }
            return v
        }
        let host = try value("host")
        guard let port = Int(try value("port")), (1...65535).contains(port) else {
            throw PairingError.badPort
        }
        return Pairing(host: host, port: port, token: try value("token"), fingerprint: try value("fp"))
    }
}

extension Data {
    /// sha256Base64URL is the base64url (no padding) SHA-256 of the bytes: the
    /// form a pairing's fingerprint takes.
    var sha256Base64URL: String {
        Data(SHA256.hash(data: self)).base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }
}
