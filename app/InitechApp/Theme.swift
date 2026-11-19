import InitechCore
import SwiftUI

/// Theme is the app's look: a terminal's. Near-black, monospaced, one red for
/// what can be pressed, and a colour per pane state. Every screen takes its
/// colours and fonts from here, so the look changes in one place.
enum Theme {
    static let bg = Color(red: 0.027, green: 0.031, blue: 0.039)
    /// bar is the background of the bars at the screen's edges.
    static let bar = Color(red: 0.047, green: 0.055, blue: 0.071)
    static let line = Color(red: 0.106, green: 0.118, blue: 0.141)
    static let bright = Color.white
    static let text = Color(red: 0.843, green: 0.855, blue: 0.878)
    /// soft is secondary text that must still be read; dim is the quietest.
    static let soft = Color(red: 0.604, green: 0.627, blue: 0.667)
    static let dim = Color(red: 0.424, green: 0.447, blue: 0.490)
    /// faint is the empty part of a meter.
    static let faint = Color(red: 0.149, green: 0.165, blue: 0.192)
    static let red = Color(red: 1.0, green: 0.353, blue: 0.302)
    static let green = Color(red: 0.188, green: 0.820, blue: 0.345)
    static let blue = Color(red: 0.290, green: 0.639, blue: 1.0)
    static let orange = Color(red: 1.0, green: 0.624, blue: 0.039)
    static let grey = Color(red: 0.627, green: 0.627, blue: 0.667)

    /// mono is the app's typeface at a text style, so it follows the text size
    /// he set on the device.
    static func mono(_ style: Font.TextStyle, _ weight: Font.Weight = .regular) -> Font {
        .system(style, design: .monospaced).weight(weight)
    }
}

/// ContextBarView is a pane's context as ten blocks, lit up to its percentage
/// and orange once it runs high.
struct ContextBarView: View {
    let percent: Int

    var body: some View {
        let meter = ContextMeter(percent: percent)
        HStack(spacing: 1.5) {
            ForEach(0..<ContextMeter.blocks, id: \.self) { i in
                Rectangle()
                    .fill(i < meter.filled ? (meter.high ? Theme.orange : Theme.green) : Theme.faint)
                    .frame(width: 5, height: 10)
            }
        }
        .accessibilityHidden(true)
    }
}
