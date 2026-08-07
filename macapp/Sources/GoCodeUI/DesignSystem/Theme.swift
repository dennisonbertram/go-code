import AppKit
import SwiftUI

/// A single grey value shared between the two system appearances. Kept as
/// plain 0–255 RGB rather than a `Color` so `ThemeTests` can assert on the
/// exact numbers (span, neutrality, ramp order) without resolving a dynamic
/// `NSColor` at runtime — the `Color` tokens below are derived from the same
/// values, so a regression here is a regression there too.
struct RGB: Equatable {
    let r: Int
    let g: Int
    let b: Int
    /// Codex's surfaces measure R=G=B; GoCode's old `.quaternary` ramp
    /// measured R=B+3 on every surface (`.ux/design-baseline.md` §8). This is
    /// the check that catches a hue creeping back in.
    var isNeutral: Bool { r == g && g == b }
    /// Mean of the channels — equal to any channel when `isNeutral`, and the
    /// perceptual grey level used for span/ordering comparisons either way.
    var level: Int { (r + g + b) / 3 }
    var unitWhite: Double { Double(level) / 255 }
}

struct GreyLevel {
    let dark: RGB
    let light: RGB
}

/// Semantic color tokens for GoCodeUI.
///
/// Before this file existed, every surface in the app was the same
/// `.quaternary` system material at a different opacity over the window
/// background (`AppShell.swift`'s old `.background(.quaternary.opacity(0.25))`
/// / `0.35`, `CardStyle.swift`'s `.background(.quaternary.opacity(opacity))`).
/// Stacking flat opacities of one material over one background caps how far
/// apart two surfaces can ever read, however many opacities you try: six
/// surfaces measured only 13 grey levels apart (40→53), and `.quaternary`
/// itself carries a slight warm tint on macOS, so every one of them measured
/// `R = B + 3` instead of neutral (`.ux/design-baseline.md` §8).
///
/// These tokens are opaque, explicit, neutral RGB levels instead, chosen to
/// land near the reference points measured from Codex (ChatGPT.app) in that
/// baseline — a 24→51 span across four surface tiers, and a foreground ramp
/// with more than two usable rungs. They are not pixel-exact copies of
/// Codex's numbers: this is GoCode's own palette, sized to the same kind of
/// separation Codex has, not a clone of it.
enum Theme {

    // MARK: - Surfaces
    // Dark values are a 27-level span (24→51), matching Codex's measured
    // 24→34→36→45→51. The 36 rung is the transcript's dedicated neutral
    // message surface; the chrome hierarchy itself stays at four tiers. Light values
    // do not mirror the dark numbers (255−x would make elevated surfaces
    // *darker* than the page, which reads backwards in a light appearance):
    // they keep the same "further from paper white = further back"
    // relationship the dark ramp expresses in the other direction.

    /// The window's own background — the furthest-back surface. Nothing
    /// should render directly on this except the transcript/list content
    /// itself.
    static let backgroundLevel = GreyLevel(
        dark: RGB(r: 24, g: 24, b: 24), light: RGB(r: 246, g: 246, b: 246))

    /// One step up: rails, side panels, footer/status bars — chrome that sits
    /// beside primary content rather than on top of it.
    static let surfaceLevel = GreyLevel(
        dark: RGB(r: 34, g: 34, b: 34), light: RGB(r: 250, g: 250, b: 250))

    /// Two steps up: cards, input fields, popups, code blocks — anything that
    /// reads as its own control or container floating over `surface`.
    static let surfaceElevatedLevel = GreyLevel(
        dark: RGB(r: 45, g: 45, b: 45), light: RGB(r: 255, g: 255, b: 255))

    /// The frontmost neutral tier: selected/active rows and small tags that
    /// need to read as in front of an already-elevated surface.
    static let surfaceHighestLevel = GreyLevel(
        dark: RGB(r: 51, g: 51, b: 51), light: RGB(r: 235, g: 235, b: 235))

    /// The transcript's user message is intentionally an ownership-neutral
    /// #242424 surface in dark mode. It is distinct from app chrome so the
    /// conversation reads as writing rather than a tinted chat bubble.
    static let messageSurfaceLevel = GreyLevel(
        dark: RGB(r: 36, g: 36, b: 36), light: RGB(r: 240, g: 240, b: 240))

    /// A rail selection is navigation state, not an accent-bearing action.
    /// Keeping it as a semantic alias makes that distinction durable while
    /// deliberately sharing the frontmost neutral surface rung.
    static let selectedRowSurfaceLevel = surfaceHighestLevel

    // MARK: - Foreground
    // Four rungs (the task's stated minimum; Codex measures five —
    // 255/222/150/139/116/97 — six rungs, matching the reference's ramp
    // span). Light values are `255 − dark`, so each rung keeps the same
    // *distance from its appearance's extreme* — same relative contrast
    // step — instead of the two appearances drifting to different ratios.

    /// Primary body text — what you read. Baseline measured Codex's body
    /// contrast at 17.8:1 against GoCode's old 10.8:1; matching Codex's own
    /// black/white extremes is how that gap closes.
    static let foregroundLevel = GreyLevel(
        dark: RGB(r: 255, g: 255, b: 255), light: RGB(r: 0, g: 0, b: 0))

    /// Secondary — labels that matter but are not the thing you're reading:
    /// status words, model names, sidebar-equivalent labels.
    static let foregroundSecondaryLevel = GreyLevel(
        dark: RGB(r: 222, g: 222, b: 222), light: RGB(r: 33, g: 33, b: 33))

    /// Tertiary — metadata: dates, counts, durations, captions, chip labels.
    ///
    /// 150, not 163. The reference's ramp has no value at 163 (`#A3A3A3`) at
    /// all; a critic measuring both apps found we were the only one using it.
    /// 150 is `#969696`, which is where the reference puts this rung.
    static let foregroundTertiaryLevel = GreyLevel(
        dark: RGB(r: 150, g: 150, b: 150), light: RGB(r: 92, g: 92, b: 92))

    /// Subtle — overflow menus, panel affordances, and other controls that
    /// should be found when looked for and ignored otherwise.
    ///
    /// This rung existed in the reference and not here, which is why seven
    /// different chrome elements had collapsed into one 220–224 band: with
    /// nothing between secondary and quaternary, everything rounded up to
    /// secondary.
    static let foregroundSubtleLevel = GreyLevel(
        dark: RGB(r: 139, g: 139, b: 139), light: RGB(r: 108, g: 108, b: 108))

    /// Quaternary — section headers and other labels that name a group
    /// without competing with it. `#747474`, matching the reference exactly.
    static let foregroundQuaternaryLevel = GreyLevel(
        dark: RGB(r: 116, g: 116, b: 116), light: RGB(r: 139, g: 139, b: 139))

    /// Placeholder — the dimmest still-legible text. `#616161`, which the
    /// reference uses for composer placeholder text and nothing else.
    static let foregroundPlaceholderLevel = GreyLevel(
        dark: RGB(r: 97, g: 97, b: 97), light: RGB(r: 154, g: 154, b: 154))

    /// Selected navigation labels remain primary content, rather than taking
    /// on the system tint that is reserved for an explicit product action.
    static let selectedRowForegroundLevel = foregroundLevel

    // MARK: - Color tokens
    // Built from an appearance-aware `NSColor` so one `Color` constant tracks
    // the system's current appearance instead of the app needing an
    // `@Environment(\.colorScheme)` read at every call site.

    private static func color(_ level: GreyLevel) -> Color {
        Color(
            NSColor(name: nil) { appearance in
                let isDark = appearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
                let unit = isDark ? level.dark.unitWhite : level.light.unitWhite
                return NSColor(white: unit, alpha: 1)
            })
    }

    static let background = color(backgroundLevel)
    static let surface = color(surfaceLevel)
    static let surfaceElevated = color(surfaceElevatedLevel)
    static let surfaceHighest = color(surfaceHighestLevel)
    static let messageSurface = color(messageSurfaceLevel)
    static let selectedRowSurface = color(selectedRowSurfaceLevel)

    static let foreground = color(foregroundLevel)
    static let foregroundSecondary = color(foregroundSecondaryLevel)
    static let foregroundTertiary = color(foregroundTertiaryLevel)
    static let foregroundSubtle = color(foregroundSubtleLevel)
    static let foregroundQuaternary = color(foregroundQuaternaryLevel)
    static let foregroundPlaceholder = color(foregroundPlaceholderLevel)
    static let selectedRowForeground = color(selectedRowForegroundLevel)

    /// Hairline dividers for boundaries between surfaces close enough in
    /// value that a boundary would not otherwise read (baseline §8: adjacent
    /// GoCode surfaces used to differ by only 2–3 levels — below the
    /// threshold where a value jump alone reads as a seam).
    /// Hairlines recede into the content surface instead of outlining every
    /// transcript region. The dark value is the measured #2B2B2B reference.
    static let separatorLevel = GreyLevel(
        dark: RGB(r: 43, g: 43, b: 43), light: RGB(r: 220, g: 220, b: 220))
    static let separator = color(separatorLevel)

    /// A rule that reads as a rule.
    ///
    /// 60, not 43. A border must be *lighter* than the surface it borders or
    /// it is indistinguishable from the antialiased edge of a rounded rect —
    /// which is exactly what happened: a 43 edge against a 45 fill was
    /// reported as a border and was not one. The reference's is 60 against the
    /// same 45 fill.
    static let ruleLevel = GreyLevel(
        dark: RGB(r: 60, g: 60, b: 60), light: RGB(r: 198, g: 198, b: 198))
    static let rule = color(ruleLevel)

    /// The sidebar's own separator, one step below `rule` because it divides
    /// two areas of the same surface rather than lifting a card off the page.
    static let railRuleLevel = GreyLevel(
        dark: RGB(r: 57, g: 57, b: 57), light: RGB(r: 205, g: 205, b: 205))
    static let railRule = color(railRuleLevel)

    /// The column divider between sidebar and content. 67, not the 47 the
    /// system Divider was drawing — it was the one rule of the four whose
    /// colour did not match the reference.
    static let columnDividerLevel = GreyLevel(
        dark: RGB(r: 67, g: 67, b: 67), light: RGB(r: 196, g: 196, b: 196))
    static let columnDivider = color(columnDividerLevel)

    /// Unchanged from the system tint. The baseline's accent-related gap
    /// (§3, §10 — GoCode spends its one saturated hue on message ownership
    /// rather than run state) is a separate remedy from this task's palette
    /// fix; this token exists so call sites read `Theme.accent` instead of a
    /// bare `Color.accentColor` scattered across eight files.
    static let accent = Color.accentColor
}
