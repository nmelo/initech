import AVFoundation
import SwiftUI

/// PairingView is the token screen: scan the QR `initech app-serve token`
/// shows on the Mac, or paste the same pairing line it prints as text.
struct PairingView: View {
    let pair: (String) throws -> Void
    @State private var pasted = ""
    @State private var problem: String?

    var body: some View {
        VStack(spacing: 20) {
            Text("Pair with your Mac").font(.title2.bold())
            Text("On the Mac, run initech app-serve token and scan the code it shows.")
                .multilineTextAlignment(.center)
                .foregroundStyle(.secondary)
            QRScanner { code in submit(code) }
                .frame(maxWidth: 320, maxHeight: 320)
                .clipShape(RoundedRectangle(cornerRadius: 16))
            TextField("or paste the pairing line", text: $pasted)
                .textFieldStyle(.roundedBorder)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .onSubmit { submit(pasted) }
            if let problem {
                Text(problem).foregroundStyle(.red).font(.footnote)
            }
        }
        .padding()
    }

    private func submit(_ code: String) {
        do {
            try pair(code)
        } catch {
            problem = PairingView.shown(error)
        }
    }

    /// shown logs a pairing failure the screen is about to show and returns
    /// the line to show.
    static func shown(_ error: Error) -> String {
        Log.shown(error, screen: "pairing")
        return describe(error)
    }

    /// describe turns a pairing failure into the one line the screen shows.
    static func describe(_ error: Error) -> String {
        switch error {
        case PairingError.notAPairingCode: "That is not an initech pairing code."
        case PairingError.missing(let field): "The pairing code has no \(field)."
        case PairingError.badPort: "The pairing code has a bad port."
        default: "The pairing could not be saved (\(error))."
        }
    }
}

/// QRScanner shows the camera and reports the first QR code it reads. With no
/// camera (the simulator), it shows a plain box and the paste field does the work.
struct QRScanner: UIViewControllerRepresentable {
    let found: (String) -> Void

    func makeUIViewController(context: Context) -> ScannerController {
        let c = ScannerController()
        c.found = found
        return c
    }

    func updateUIViewController(_ controller: ScannerController, context: Context) {}

    final class ScannerController: UIViewController, @preconcurrency AVCaptureMetadataOutputObjectsDelegate {
        var found: ((String) -> Void)?
        private let session = AVCaptureSession()
        private var reported = false

        override func viewDidLoad() {
            super.viewDidLoad()
            view.backgroundColor = .secondarySystemBackground
            guard let camera = AVCaptureDevice.default(for: .video),
                  let input = try? AVCaptureDeviceInput(device: camera),
                  session.canAddInput(input) else { return }
            session.addInput(input)
            let output = AVCaptureMetadataOutput()
            guard session.canAddOutput(output) else { return }
            session.addOutput(output)
            output.setMetadataObjectsDelegate(self, queue: .main)
            output.metadataObjectTypes = [.qr]
            let preview = AVCaptureVideoPreviewLayer(session: session)
            preview.videoGravity = .resizeAspectFill
            preview.frame = view.bounds
            view.layer.addSublayer(preview)
            // The delegate is set on the main queue above, so @preconcurrency
            // holds; startRunning blocks, so it runs off the main thread.
            nonisolated(unsafe) let session = self.session
            DispatchQueue.global(qos: .userInitiated).async { session.startRunning() }
        }

        override func viewDidLayoutSubviews() {
            super.viewDidLayoutSubviews()
            view.layer.sublayers?.forEach { $0.frame = view.bounds }
        }

        override func viewWillDisappear(_ animated: Bool) {
            super.viewWillDisappear(animated)
            session.stopRunning()
        }

        func metadataOutput(_ output: AVCaptureMetadataOutput,
                            didOutput objects: [AVMetadataObject],
                            from connection: AVCaptureConnection) {
            guard !reported,
                  let code = (objects.first as? AVMetadataMachineReadableCodeObject)?.stringValue
            else { return }
            reported = true
            found?(code)
        }
    }
}
