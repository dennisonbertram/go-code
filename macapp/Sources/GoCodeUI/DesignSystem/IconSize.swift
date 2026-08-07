import SwiftUI

/// Icon measurements are roles because a status dot, row glyph, and empty
/// state communicate at different distances. They retain existing metrics.
enum IconSize {
    static let rule: CGFloat = 3
    static let status: CGFloat = 7
    static let detail: CGFloat = 14
    static let standard: CGFloat = 15
    /// 15, not 18. Measured ink width the nav icon drew 21.0pt against the
    /// reference's 15.0 — 40% oversized, and starting 4.5pt further left.
    static let row: CGFloat = 15
    /// Composer chip icons. Fixed so adjacent chips cannot differ in symbol
    /// width, which they did by 48%.
    static let chip: CGFloat = 13.5
    static let emptyState: CGFloat = 30
    static let launch: CGFloat = 44
}
