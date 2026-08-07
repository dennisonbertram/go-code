import SwiftUI

/// Semantic type roles separate content purpose from the system style that
/// implements it. Their metrics track the readable Codex transcript scale, so
/// a density change reaches every screen through one hierarchy.
enum Typography {
    static let display = Font.system(size: 22)
    static let title = Font.system(size: 20)
    static let heading = Font.system(size: 18)
    /// Primary transcript text measures 16.5pt in the Codex reference.
    static let bodyPointSize: CGFloat = 16.5
    static let body = Font.system(size: bodyPointSize)
    /// The nominal one-line height of the `body` role on macOS.
    static let bodyLineHeight: CGFloat = 21.5
    /// Extra leading for running transcript prose, carrying `bodyLineHeight`
    /// up to the 26.5pt line pitch measured in the Codex reference. Added as
    /// leading rather than baked into the font size, because the size is
    /// already right and it was only the rhythm that was tight. Applies
    /// between lines only, so single-line rows keep `bodyLineHeight` and the
    /// derived user-message height is unaffected.
    static let bodyLinePitch: CGFloat = 26.5
    static let bodyLineSpacing: CGFloat = bodyLinePitch - bodyLineHeight
    /// 16, not 14. Measured against the reference ascender-to-descender, the
    /// roles built on this rung ran short: composer placeholder 12.0pt against
    /// 15.0, sidebar section header 10.0 against 14.5. The rung was the cause,
    /// not the call sites.
    static let caption = Font.system(size: 16)
    static let detail = Font.system(size: 12)
    static let code = Font.system(size: 15).monospaced()
    static let codeCaption = Font.system(size: 14).monospaced()
    static let codeDetail = Font.system(size: 12).monospaced()
    static let numericCaption = Font.system(size: 14).monospacedDigit()
}
