import Foundation
import Testing

@testable import GoCodeUI

@Suite("Transcript feature reachability")
struct TranscriptFeatureReachabilityTests {

    @Test("usage and whole-conversation copy retain production call sites")
    func featuresHaveProductionCallSites() throws {
        let sourceDirectory = URL(filePath: #filePath)
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .appending(path: "Sources/GoCodeUI")
        let source = try FileManager.default
            .contentsOfDirectory(at: sourceDirectory, includingPropertiesForKeys: nil)
            .filter { $0.pathExtension == "swift" }
            .map { try String(contentsOf: $0, encoding: .utf8) }
            .joined(separator: "\n")

        #expect(source.contains("UsageLabel(usage: usage)"))
        #expect(source.contains("TranscriptText.plain(items)"))
    }

    /// Scans the whole module rather than named files. These treatments are
    /// meant to exist *somewhere* in the UI; pinning them to AppShell.swift
    /// made the test fail the moment the sidebar was extracted into its own
    /// file, which is a refactor, not a regression. Module-wide still catches
    /// the thing worth catching — a treatment being dropped entirely.
    @Test("rail selection and user prompts retain their semantic layout tokens")
    func transcriptAndRailUseSemanticTokens() throws {
        let sourceDirectory = URL(filePath: #filePath)
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .appending(path: "Sources/GoCodeUI")
        let source = try FileManager.default
            .contentsOfDirectory(at: sourceDirectory, includingPropertiesForKeys: nil)
            .filter { $0.pathExtension == "swift" }
            .map { try String(contentsOf: $0, encoding: .utf8) }
            .joined(separator: "\n")

        #expect(source.contains("Theme.selectedRowSurface"))
        #expect(source.contains("Theme.selectedRowForeground"))
        #expect(source.contains("Theme.surface.ignoresSafeArea(.container, edges: .top)"))
        #expect(source.contains("Layout.userMessageMaximumWidth"))
        #expect(source.contains(".foregroundStyle(Theme.foreground)"))
    }

    /// The transcript once rendered at the macOS 13pt system default because
    /// its message views set no font at all. Every token test still passed —
    /// the scale was correct and simply unread — so raising the type scale
    /// changed nothing on screen. These assert the transcript is actually
    /// wired to the scale, which is the part that silently broke.
    /// Copy-message and copy-conversation both rendered `doc.on.doc`, so the
    /// action row showed the same glyph twice and read as a duplicated button.
    /// A critic found this two rounds before it was fixed, so it is pinned.
    @Test("the two copy actions do not share a glyph")
    func copyActionsAreDistinguishable() throws {
        let chatView = try String(
            contentsOf: URL(filePath: #filePath)
                .deletingLastPathComponent()
                .deletingLastPathComponent()
                .deletingLastPathComponent()
                .appending(path: "Sources/GoCodeUI/ChatView.swift"),
            encoding: .utf8)

        let glyphs =
            chatView
            .components(separatedBy: "Image(systemName: copied ? \"checkmark\" : ")
            .dropFirst()
            .compactMap { $0.split(separator: ")").first.map(String.init) }

        #expect(glyphs.count == 2)
        #expect(Set(glyphs).count == glyphs.count)
    }

    /// The window wore a full-width band in the sidebar's colour down to y=52,
    /// because a visible toolbar background paints one colour across the whole
    /// window. Each column must paint its own colour to the top instead.
    @Test("neither column defers its top strip to a coloured toolbar")
    func columnsPaintTheirOwnTopStrip() throws {
        let sourceDirectory = URL(filePath: #filePath)
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .appending(path: "Sources/GoCodeUI")
        let source = try FileManager.default
            .contentsOfDirectory(at: sourceDirectory, includingPropertiesForKeys: nil)
            .filter { $0.pathExtension == "swift" }
            .map { try String(contentsOf: $0, encoding: .utf8) }
            .joined(separator: "\n")

        #expect(source.contains(".toolbarBackground(.hidden, for: .windowToolbar)"))
        #expect(!source.contains(".toolbarBackground(Theme.surface, for: .windowToolbar)"))
        // Both columns reach the top on their own.
        #expect(source.contains("Theme.surface.ignoresSafeArea(.container, edges: .top)"))
        #expect(source.contains("Theme.background.ignoresSafeArea(.container, edges: .top)"))
    }

    @Test("transcript prose is bound to the shared type scale")
    func transcriptConsumesTypeScale() throws {
        let chatView = try String(
            contentsOf: URL(filePath: #filePath)
                .deletingLastPathComponent()
                .deletingLastPathComponent()
                .deletingLastPathComponent()
                .appending(path: "Sources/GoCodeUI/ChatView.swift"),
            encoding: .utf8)

        #expect(chatView.contains(".font(Typography.body)"))
        #expect(chatView.contains(".lineSpacing(Typography.bodyLineSpacing)"))
    }

    @Test("transcript leading resolves to the reference line pitch")
    func lineSpacingMatchesReferencePitch() {
        #expect(Typography.bodyLineSpacing == Typography.bodyLinePitch - Typography.bodyLineHeight)
        #expect(Typography.bodyLinePitch == 26.5)
    }
}
