import SwiftUI

/// Corner roles describe the container's job, rather than making callers
/// decide a radius independently. Values intentionally match the current UI.
enum CornerRadius {
    static let tag: CGFloat = 4
    static let code: CGFloat = 6
    /// Measured against the reference by edge-inset profile: its selected
    /// sidebar row solves to r≈10 on a 36pt row where ours solved to r≈6, and
    /// its user bubble to r≈18 where ours solved to r≈10. Both of the app's
    /// smallest surfaces were ~40% short, which is what made them read as
    /// boxes rather than as the reference's softer shapes.
    static let control: CGFloat = 10
    static let card: CGFloat = 18
    static let composer: CGFloat = 24
}
