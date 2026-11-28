// The parts of vex:crypto that come from CryptoKit on Apple systems,
// exposed to system.c as C functions. Each returns 0 on success and -1
// when it fails or the input is rejected.

import CryptoKit
import Foundation

/// Returns the [n] bytes at [p] as Data, without copying them.
private func view(_ p: UnsafeRawPointer?, _ n: Int) -> Data {
    guard let p = p, n > 0 else { return Data() }
    return Data(bytesNoCopy: UnsafeMutableRawPointer(mutating: p), count: n, deallocator: .none)
}

/// Copies [data] to [out].
private func copy<D: ContiguousBytes>(_ data: D, to out: UnsafeMutablePointer<UInt8>?) {
    data.withUnsafeBytes { src in
        if let base = src.baseAddress, let out = out, src.count > 0 {
            out.update(from: base.assumingMemoryBound(to: UInt8.self), count: src.count)
        }
    }
}

@_cdecl("vxc_ck_gcm_seal")
public func vxcGcmSeal(
    _ key: UnsafePointer<UInt8>, _ iv: UnsafePointer<UInt8>, _ ivlen: Int, _ aad: UnsafePointer<UInt8>?, _ aadlen: Int,
    _ input: UnsafePointer<UInt8>?, _ n: Int, _ out: UnsafeMutablePointer<UInt8>?, _ tag: UnsafeMutablePointer<UInt8>
) -> Int32 {
    do {
        let nonce = try AES.GCM.Nonce(data: view(iv, ivlen))
        let box = try AES.GCM.seal(view(input, n), using: SymmetricKey(data: view(key, 32)), nonce: nonce,
                                   authenticating: view(aad, aadlen))
        guard box.ciphertext.count == n, box.tag.count == 16 else { return -1 }
        copy(box.ciphertext, to: out)
        copy(box.tag, to: tag)
        return 0
    } catch {
        return -1
    }
}

@_cdecl("vxc_ck_gcm_open")
public func vxcGcmOpen(
    _ key: UnsafePointer<UInt8>, _ iv: UnsafePointer<UInt8>, _ ivlen: Int, _ aad: UnsafePointer<UInt8>?, _ aadlen: Int,
    _ input: UnsafePointer<UInt8>?, _ n: Int, _ tag: UnsafePointer<UInt8>, _ out: UnsafeMutablePointer<UInt8>?
) -> Int32 {
    do {
        let nonce = try AES.GCM.Nonce(data: view(iv, ivlen))
        let box = try AES.GCM.SealedBox(nonce: nonce, ciphertext: view(input, n), tag: view(tag, 16))
        let plain = try AES.GCM.open(box, using: SymmetricKey(data: view(key, 32)), authenticating: view(aad, aadlen))
        guard plain.count == n else { return -1 }
        copy(plain, to: out)
        return 0
    } catch {
        return -1
    }
}

@_cdecl("vxc_ck_hkdf_sha256")
public func vxcHkdfSha256(
    _ ikm: UnsafePointer<UInt8>?, _ ikmlen: Int, _ salt: UnsafePointer<UInt8>?, _ saltlen: Int,
    _ info: UnsafePointer<UInt8>?, _ infolen: Int, _ out: UnsafeMutablePointer<UInt8>, _ n: Int
) -> Int32 {
    guard n > 0, n <= 255 * 32 else { return -1 }
    let key = HKDF<SHA256>.deriveKey(inputKeyMaterial: SymmetricKey(data: view(ikm, ikmlen)), salt: view(salt, saltlen),
                                     info: view(info, infolen), outputByteCount: n)
    guard key.bitCount == n * 8 else { return -1 }
    copy(key, to: out)
    return 0
}
