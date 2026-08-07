import SwiftUI

/// Fixed dimensions describe product constraints, not spacing. Naming them
/// prevents unrelated panes and fields from becoming accidental substitutes.
enum Layout {
    static let appMinimumWidth: CGFloat = 1_040
    static let appMinimumHeight: CGFloat = 600
    /// 310, not 220. The reference's sidebar is 348.5pt — 20.1% of its
    /// window against our 16.1% — and at 220 ours failed on both the absolute
    /// and the proportional measure. 310 closes most of that without starving
    /// the transcript column, which is already width-matched to the reference.
    static let railWidth: CGFloat = 310
    static let railRowHeight: CGFloat = 37
    /// Codex's environment surface is a compact card, not a sidebar. 361pt was
    /// measured from the reference, but the reference fills only 64% of its
    /// pane with content and so has room to float the card over the margin.
    /// This app fills 94%, so at that width the card sat on top of the
    /// transcript. It is a sibling column here rather than an overlay, and
    /// narrow enough that opening it costs the transcript little.
    static let inspectorCardWidth: CGFloat = 217
    static let chatMinimumWidth: CGFloat = 400
    static let chatIdealWidth: CGFloat = 520
    /// Keep the transcript and composer on the same readable column instead
    /// of turning either into a near-edge-to-edge control on wide windows.
    static let chatContentMaximumWidth: CGFloat = 883

    /// User prompts read as authored content, not a full-column alert. This
    /// caps a wrapped prompt while allowing short prompts to retain their
    /// intrinsic width.
    static let userMessageMaximumWidth: CGFloat = 374.5

    /// A one-line body prompt plus its semantic vertical padding. Keeping this
    /// formula coupled to the same roles used by `UserBubble` lets type or
    /// spacing changes update its minimum rhythm together.
    static let userMessageMinimumHeight: CGFloat =
        Typography.bodyLineHeight + (Spacing.userMessageVertical * 2)
    static let providerMinimumWidth: CGFloat = 240
    static let providerIdealWidth: CGFloat = 280
    static let modelMinimumWidth: CGFloat = 380
    static let costFieldWidth: CGFloat = 54
    static let diffGutterWidth: CGFloat = 38
    static let emptyStateTextWidth: CGFloat = 320
    static let providerSheetWidth: CGFloat = 460
    /// Placeholder heights mirror the corresponding rows so completed fetches
    /// replace a shape instead of changing the surrounding layout.
    static let loadingRowHeight: CGFloat = 34
    static let conversationRowHeight: CGFloat = 44
    static let checkpointRowHeight: CGFloat = 52
    static let modelProviderRowHeight: CGFloat = 52
    static let modelSettingsRowHeight: CGFloat = 40
    static let inlineActivitySlot: CGFloat = IconSize.standard
    static let loadingPlaceholderRowCount = 4
}
