import SwiftUI
import Testing

@testable import GoCodeUI

@Suite("Non-colour design tokens")
struct DesignTokenTests {

    @Test("the brand mark uses the selected D0 ring and inward chevron")
    func brandMarkUsesD0Geometry() {
        let path = BrandMark().path(in: CGRect(x: 0, y: 0, width: 100, height: 100))
        var elements: [Path.Element] = []
        func append(_ element: Path.Element) {
            elements.append(element)
        }
        path.forEach(append)

        guard elements.count == 8,
            case .move(let ringStart) = elements[0],
            case .curve(let ringEnd, control1: _, control2: _) = elements[4],
            case .move(let chevronStart) = elements[5],
            case .line(let chevronPoint) = elements[6],
            case .line(let chevronEnd) = elements[7]
        else {
            Issue.record("BrandMark path no longer has the expected D0 arc and chevron elements")
            return
        }

        // D0's SVG reference uses a 23.5pt ring centered at (50, 50), with
        // right-side endpoints at approximately (72.3, 43) and (72.3, 57).
        #expect(abs(ringStart.x - 72.4) < 0.15)
        #expect(abs(ringStart.y - 43.0) < 0.15)
        #expect(abs(ringEnd.x - 72.4) < 0.15)
        #expect(abs(ringEnd.y - 57.0) < 0.15)

        // The chevron is mirrored around the vertical centerline and points
        // inward from the right edge of the ring.
        #expect(chevronStart == CGPoint(x: 57, y: 42.5))
        #expect(chevronPoint == CGPoint(x: 74, y: 50))
        #expect(chevronEnd == CGPoint(x: 57, y: 57.5))
    }

    @Test("spacing keeps the established layout rhythm")
    func spacingRhythm() {
        #expect(Spacing.none == 0)
        #expect(Spacing.tight == 2)
        #expect(Spacing.compact == 4)
        #expect(Spacing.small == 6)
        #expect(Spacing.standard == 8)
        #expect(Spacing.comfortable == 10)
        #expect(Spacing.inset == 12)
        #expect(Spacing.large == 16)
        #expect(Spacing.section == 18)
    }

    /// Radii updated in round 11: control 8 → 10, card 10 → 18, composer
    /// 20 → 24. The previous values were this app's own, not the reference's;
    /// an edge-inset profile put both of the smallest surfaces ~40% short.
    @Test("shape and icon roles preserve their current measurements")
    func shapeAndIconMeasurements() {
        #expect(CornerRadius.tag == 4)
        #expect(CornerRadius.code == 6)
        #expect(CornerRadius.control == 10)
        #expect(CornerRadius.card == 18)
        #expect(CornerRadius.composer == 24)
        #expect(IconSize.status == 7)
        #expect(IconSize.detail == 14)
        #expect(IconSize.standard == 15)
        // 15, not 18: measured ink width was 40% over the reference's.
        #expect(IconSize.row == 15)
        #expect(IconSize.emptyState == 30)
        #expect(IconSize.launch == 44)
    }

    /// The environment inspector is a compact card, not a window-height split.
    /// Keeping its footprint in the token layer prevents a future layout pass
    /// from quietly turning it back into a sidebar.
    ///
    /// 361pt was the reference's own width, but the reference floats its card
    /// over a pane that is only 64% filled. This app fills 94%, so at that
    /// width the card covered the transcript it describes. It is now a sibling
    /// column at 60% of that width.
    @Test("environment inspector retains its compact card footprint")
    func environmentInspectorFootprint() {
        #expect(Layout.inspectorCardWidth == 217)
        // Narrow enough that opening it leaves the transcript readable.
        #expect(Layout.inspectorCardWidth < Layout.chatContentMaximumWidth / 3)
    }

    /// Five compact rows once needed 249pt inside the rail's 204pt content
    /// width. The overflow widened the column and pushed every row off-centre,
    /// so the selected pill rendered 1pt from the left edge and 15pt from the
    /// right. This pins the arithmetic that keeps the footer inside its column.
    @Test("the compact rail footer fits inside the rail's content width")
    func compactRailFooterFits() {
        let iconRow = IconSize.row
        let compactRowWidth = iconRow + (Spacing.small * 2)
        let footer = (compactRowWidth * 5) + (Spacing.tight * 4)
        let available = Layout.railWidth - (Spacing.standard * 2)
        #expect(footer <= available)
    }

    @Test("loading placeholders use named row and inline-control geometry")
    func loadingGeometryIsTokenized() {
        #expect(Layout.loadingRowHeight > Spacing.large)
        #expect(Layout.inlineActivitySlot == IconSize.standard)
    }

    /// The user-message row tracks its text role and vertical rhythm instead
    /// of preserving an unexplained screenshot measurement.
    @Test("transcript message height derives from its type and spacing roles")
    func transcriptMessageSurface() {
        #expect(
            Layout.userMessageMinimumHeight
                == Typography.bodyLineHeight + (Spacing.userMessageVertical * 2))
        #expect(Layout.userMessageMinimumHeight == 45.5)
        #expect(Layout.userMessageMaximumWidth == 374.5)
        #expect(Typography.bodyPointSize == 16.5)
        #expect(Typography.bodyLineHeight == 21.5)
        #expect(Theme.messageSurfaceLevel.dark == RGB(r: 36, g: 36, b: 36))
        #expect(Theme.messageSurfaceLevel.dark.isNeutral)
    }

    @Test("selected rail rows use the neutral selected-row tokens")
    func selectedRailTokens() {
        #expect(Theme.selectedRowSurfaceLevel.dark == RGB(r: 51, g: 51, b: 51))
        #expect(Theme.selectedRowSurfaceLevel.dark.isNeutral)
        #expect(Theme.selectedRowForegroundLevel.dark == RGB(r: 255, g: 255, b: 255))
    }

    @Test("conversation layout tokens share the Codex column and quieter divider")
    func conversationLayout() {
        #expect(Layout.chatContentMaximumWidth == 883)
        // 40, not 52: with padding the band measured 103pt against the
        // reference's 55pt.
        #expect(Spacing.conversationHeaderHeight == 40)
        // 38, not 65.5: the reference opens its transcript tight and
        // spends the space below the bubble instead.
        #expect(Spacing.transcriptTop == 38)
        #expect(Theme.separatorLevel.dark == RGB(r: 43, g: 43, b: 43))
    }
}

extension DesignTokenTests {
    /// The mark is drawn, not shipped as an image, so its geometry is the
    /// thing that can regress. These pin the two properties that decide
    /// whether it survives at 16pt, where a Dock icon actually lives.
    @Test("the brand mark keeps the stroke and radius that hold at small sizes")
    func brandMarkGeometry() {
        // A heavier stroke closes the ring's counter; a lighter one disappears.
        #expect(BrandMark.strokeRatio > 0.06)
        #expect(BrandMark.strokeRatio < 0.10)
        // The ring must leave room for the tile's corner radius around it.
        #expect(BrandMark.radiusRatio < 0.28)
    }

    /// Scale-independence is the reason for drawing it: the same Shape has to
    /// produce the same figure at a Dock icon's size and a menu glyph's.
    @Test("the brand mark scales without changing shape")
    func brandMarkIsScaleIndependent() {
        let smallRect = CGRect(x: 0, y: 0, width: 16, height: 16)
        let largeRect = CGRect(x: 0, y: 0, width: 512, height: 512)
        let small: CGRect = BrandMark().path(in: smallRect).boundingRect
        let large: CGRect = BrandMark().path(in: largeRect).boundingRect

        let smallWidthRatio: CGFloat = small.width / 16
        let largeWidthRatio: CGFloat = large.width / 512
        #expect(abs(smallWidthRatio - largeWidthRatio) < 0.01)
    }

    /// A non-square frame must not stretch it — the Dock and toolbars both
    /// hand views rectangles that are not exactly square.
    @Test("the brand mark stays square in a non-square frame")
    func brandMarkDoesNotStretch() {
        let rect = CGRect(x: 0, y: 0, width: 200, height: 80)
        let box: CGRect = BrandMark().path(in: rect).boundingRect
        let difference: CGFloat = abs(box.width - box.height)
        #expect(difference < 1.0)
    }
}

extension DesignTokenTests {
    /// Seven chrome elements had collapsed into one 220–224 band because the
    /// ramp had no rung between secondary and quaternary — everything rounded
    /// up to secondary. These pin the six distinct rungs.
    @Test("the foreground ramp has six distinct rungs")
    func foregroundRampIsSixSteps() {
        let rungs = [
            Theme.foregroundLevel.dark.r,
            Theme.foregroundSecondaryLevel.dark.r,
            Theme.foregroundTertiaryLevel.dark.r,
            Theme.foregroundSubtleLevel.dark.r,
            Theme.foregroundQuaternaryLevel.dark.r,
            Theme.foregroundPlaceholderLevel.dark.r,
        ]
        #expect(Set(rungs).count == rungs.count)
        // Strictly descending: a ramp that doubles back is not a hierarchy.
        #expect(rungs == rungs.sorted(by: >))
    }

    /// 163 (#A3A3A3) is a value the reference never uses. A critic measuring
    /// both apps found we were the only one with it, which is how an element
    /// ends up looking foreign without any single token being obviously wrong.
    @Test("the ramp avoids the value the reference never uses")
    func rampAvoidsForeignValue() {
        let rungs = [
            Theme.foregroundLevel.dark.r,
            Theme.foregroundSecondaryLevel.dark.r,
            Theme.foregroundTertiaryLevel.dark.r,
            Theme.foregroundSubtleLevel.dark.r,
            Theme.foregroundQuaternaryLevel.dark.r,
            Theme.foregroundPlaceholderLevel.dark.r,
        ]
        #expect(!rungs.contains(163))
    }

    /// Measured against the reference: section labels #747474, placeholder
    /// #616161. Both were confirmed exact by pixel probe, so they are pinned
    /// rather than left to drift.
    @Test("the ramp holds the two rungs measured exactly against the reference")
    func rampMatchesMeasuredReferenceValues() {
        #expect(Theme.foregroundQuaternaryLevel.dark.r == 116)  // #747474
        #expect(Theme.foregroundPlaceholderLevel.dark.r == 97)  // #616161
    }
}

extension DesignTokenTests {
    /// A border must be lighter than the surface it borders. A stroke at 43
    /// against a 45 fill was reported as a border and was not one — it was
    /// indistinguishable from the antialiased edge of the rounded rect
    /// underneath it. This pins the direction, not just the value.
    @Test("rule tokens are lighter than the surfaces they border")
    func rulesReadAsRules() {
        #expect(Theme.ruleLevel.dark.r > Theme.surfaceElevatedLevel.dark.r)
        #expect(Theme.railRuleLevel.dark.r > Theme.surfaceLevel.dark.r)
        // And distinctly so, rather than by a rounding error.
        #expect(Theme.ruleLevel.dark.r - Theme.surfaceElevatedLevel.dark.r >= 10)
    }

    /// Two adjacent composer chips had a 48% icon-width spread and an 85%
    /// label-gap spread because each sized its symbol from font metrics.
    /// Pinning both to tokens is what keeps them a set.
    @Test("composer chips share one icon size and one label gap")
    func chipGeometryIsTokenized() {
        #expect(IconSize.chip > 0)
        #expect(Spacing.chipLabelGap > 0)
        // Close to the reference's measured 13.5pt icon and 8.5pt gap.
        #expect(abs(IconSize.chip - 13.5) < 1.0)
        #expect(abs(Spacing.chipLabelGap - 8.5) < 1.0)
    }

    /// The sidebar was 63% of the reference's width and failed on both the
    /// absolute and the proportional measure.
    @Test("the sidebar is wide enough to read as a content browser")
    func railWidthIsCloserToTheReference() {
        #expect(Layout.railWidth >= 300)
    }
}

extension DesignTokenTests {
    /// Every rule in the app measured 2 raw px against the reference's 1 —
    /// composer border, header rule, footer rule and column divider, four for
    /// four. On a 2x display 0.5pt is the single-pixel line.
    @Test("rules are drawn at the reference's weight")
    func rulesAreHalfPoint() {
        #expect(Spacing.hairline == 0.5)
    }

    /// The column divider was the one rule of four whose colour did not match,
    /// because it was a system Divider rather than a token.
    @Test("the column divider is a token lighter than the sidebar it borders")
    func columnDividerIsTokenized() {
        #expect(Theme.columnDividerLevel.dark.r == 67)
        #expect(Theme.columnDividerLevel.dark.r > Theme.surfaceLevel.dark.r)
    }

    /// Both of the app's smallest surfaces were ~40% short of the reference's
    /// radii, which is what made them read as boxes.
    @Test("corner radii match the reference's measured curvature")
    func radiiMatchReference() {
        #expect(CornerRadius.control == 10)
        #expect(CornerRadius.card == 18)
        #expect(CornerRadius.card > CornerRadius.control)
    }
}
