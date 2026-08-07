import AppKit
import HarnessKit
import SwiftUI

/// Conversation surface with an on-demand environment card over the transcript.
struct ChatView: View {
    @Bindable var project: ProjectSession
    @Bindable var run: RunSession
    @State private var selected: ToolActivity?
    // Baseline gap #3: a permanent HSplitView pane held 44% of the window's
    // area to show one tool call's JSON. Default it closed and let it open on
    // demand instead, matching Codex's floating, give-the-space-back panel.
    @State private var showInspector = false

    var body: some View {
        // A row, not a ZStack. As an overlay the card covered the transcript it
        // was describing, because this app's content column fills 94% of the
        // pane and leaves no margin to float over. As a sibling the transcript
        // simply reflows around it.
        HStack(alignment: .top, spacing: Spacing.none) {
            VStack(spacing: Spacing.none) {
                ConversationHeader(
                    project: project, run: run, showInspector: $showInspector)
                TranscriptView(
                    items: run.transcript.items,
                    statusMessage: project.statusMessage,
                    run: run,
                    selected: $selected,
                    project: project
                )
                if let plan = run.transcript.pendingPlan, plan.runID == run.currentRunID {
                    PlanApprovalView(plan: plan, run: run)
                } else if let prompt = run.pendingQuestions, prompt.runID == run.currentRunID {
                    AskUserView(prompt: prompt, answerInFlight: run.answerInFlight) {
                        run.answer($0, expectedRunID: prompt.runID)
                    }
                } else if let approval = run.transcript.pendingApproval,
                    approval.runID == run.currentRunID
                {
                    ApprovalBar(approval: approval, run: run)
                }
                Composer(project: project, run: run)
            }
            .frame(minWidth: Layout.chatMinimumWidth, idealWidth: Layout.chatIdealWidth)

            if showInspector {
                EnvironmentInspector(
                    project: project,
                    usage: run.transcript.usage,
                    activities: toolActivities,
                    selected: $selected
                )
                .padding(.trailing, Spacing.comfortable)
                .padding(.vertical, Spacing.comfortable)
                .transition(.move(edge: .trailing).combined(with: .opacity))
            }
        }
        // The content pane paints its own colour through the top safe area,
        // the way the rail does. Previously only the toolbar background
        // covered this strip, in the *sidebar's* colour, so the pane began at
        // y=52 and the window wore a full-width band above it.
        .background(Theme.background.ignoresSafeArea(.container, edges: .top))

        // Clicking a tool call is the reason the inspector exists; open it
        // automatically on selection rather than leaving the click looking
        // like it did nothing because the pane defaults to closed.
        .onChange(of: selected) { _, activity in
            if activity != nil { showInspector = true }
        }
        .task {
            await project.syncCurrentConversation()
        }
    }

    private var toolActivities: [ToolActivity] {
        run.transcript.items.compactMap { item in
            guard case .toolActivity(let activity) = item.kind else { return nil }
            return activity
        }
    }
}

// MARK: - Transcript

struct TranscriptView: View {
    let items: [TranscriptItem]
    let statusMessage: String?
    @Bindable var run: RunSession
    @Binding var selected: ToolActivity?
    @Bindable var project: ProjectSession
    /// Auto-scroll only while the user is already at the bottom, so scrolling
    /// back to read is not yanked away mid-stream.
    @State private var pinnedToBottom = true

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                ConversationColumn {
                    // The reference leaves 62pt between a user bubble and the
                    // tool row that follows it; ours left 19.5 — 3.2x tight,
                    // and the single most visible rhythm defect.
                    LazyVStack(alignment: .leading, spacing: Spacing.transcriptTurnGap) {
                        ForEach(TranscriptPresentation.rows(for: items)) { item in
                            row(for: item).id(item.id)
                        }
                        if run.isBusy {
                            InlineRunStatus(
                                run: run,
                                statusMessage: statusMessage,
                                scheduledRunStatus: run.scheduledRunStatus)
                        }
                        Color.clear.frame(height: Spacing.hairline).id(bottomAnchor)
                    }
                    .padding(.top, Spacing.transcriptTop)
                    .padding(.bottom, Spacing.large)
                    // Primary transcript content uses the explicit foreground
                    // rung so macOS's subdued label default cannot compress the
                    // measured contrast of the shared body role.
                    .foregroundStyle(Theme.foreground)
                }
            }
            .onChange(of: items.last?.id) { _, _ in scrollIfPinned(proxy) }
            .onChange(of: lastItemLength) { _, _ in scrollIfPinned(proxy) }
        }
    }

    private let bottomAnchor = "transcript-bottom"

    /// Streaming mutates the last item in place rather than appending, so the
    /// item id alone does not change as text arrives.
    private var lastItemLength: Int {
        guard case .assistantMessage(let message) = items.last?.kind else { return 0 }
        return message.text.count
    }

    private func scrollIfPinned(_ proxy: ScrollViewProxy) {
        guard pinnedToBottom else { return }
        withAnimation(.easeOut(duration: 0.12)) {
            proxy.scrollTo(bottomAnchor, anchor: .bottom)
        }
    }

    @ViewBuilder
    private func row(for displayItem: TranscriptDisplayItem) -> some View {
        switch displayItem.kind {
        case .toolActivities(let activities):
            ToolRow(activities: activities) {
                selected = activities.last
            }
        case .item(let item):
            transcriptRow(for: item)
        }
    }

    @ViewBuilder
    private func transcriptRow(for item: TranscriptItem) -> some View {
        switch item.kind {
        case .userPrompt(let text):
            UserBubble(text: text).frame(maxWidth: .infinity, alignment: .trailing)
        case .assistantMessage(let message):
            AssistantBubble(message: message, project: project, run: run)
        case .thinking(let text):
            ThinkingRow(text: text)
        case .toolActivity:
            EmptyView()
        case .error(let message):
            ErrorRow(message: message)
        case .notice(let message):
            NoticeRow(message: message)
        case .compaction(let summary, let removed):
            CompactionRow(summary: summary, messagesRemoved: removed)
        }
    }
}

/// Collapsed by default, like the TUI's Ctrl+O block: it marks that history was
/// folded without burying the conversation in the summary.
struct CompactionRow: View {
    let summary: String
    let messagesRemoved: Int
    @State private var expanded = false

    var body: some View {
        DisclosureGroup(isExpanded: $expanded) {
            Text(summary)
                .font(Typography.body).textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.top, 5)
        } label: {
            Label(
                messagesRemoved > 0
                    ? "History compacted — \(messagesRemoved) messages folded"
                    : "History compacted",
                systemImage: "arrow.down.right.and.arrow.up.left")
        }
        .font(Typography.caption)
        .foregroundStyle(Theme.foregroundTertiary)
        .padding(Spacing.comfortable)
        .compactElevatedSurface()
    }
}

/// Copies one whole message. Dragging cannot do this job: SwiftUI text
/// selection never spans separate `Text` views, so a reply split across
/// paragraphs and code blocks can only be taken whole by a button.
struct CopyMessageButton: View {
    let text: String
    @State private var copied = false

    var body: some View {
        Button {
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(text, forType: .string)
            copied = true
            Task {
                try? await Task.sleep(for: .seconds(1.6))
                copied = false
            }
        } label: {
            Image(systemName: copied ? "checkmark" : "doc.on.doc")
                .font(Typography.caption)
                .foregroundStyle(
                    // Only the copied state overrides; the row owns the
                    // resting ink so all four icons agree.
                    copied ? AnyShapeStyle(.tint) : AnyShapeStyle(Theme.foregroundSubtle)
                )
                .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .help(copied ? "Copied" : "Copy this message")
        .accessibilityLabel("Copy message")
    }
}

/// Copies the complete transcript, including user prompts and tool activity,
/// from the conversation's primary toolbar affordances.
struct CopyConversationButton: View {
    let items: [TranscriptItem]
    @State private var copied = false

    var body: some View {
        Button {
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(TranscriptText.plain(items), forType: .string)
            copied = true
            Task {
                try? await Task.sleep(for: .seconds(1.6))
                copied = false
            }
        } label: {
            // A distinct glyph from CopyMessageButton's. Both were
            // "doc.on.doc", so the two actions rendered identically and the
            // row read as a duplicated button. This one copies the whole
            // transcript, which is a page rather than a snippet.
            Image(systemName: copied ? "checkmark" : "text.document")
        }
        .disabled(items.isEmpty)
        .help(copied ? "Copied conversation" : "Copy conversation")
        .accessibilityLabel("Copy conversation")
    }
}

struct UserBubble: View {
    let text: String

    var body: some View {
        ContentHuggingWidthLayout(maximumWidth: Layout.userMessageMaximumWidth) {
            Text(text)
                // Same reason as the assistant blocks: without an explicit
                // role this inherited the 13pt system default, so the prompt
                // rendered smaller than the reply it belongs to.
                .font(Typography.body)
                .textSelection(.enabled)
                .padding(.horizontal, Spacing.inset)
                .padding(.vertical, Spacing.userMessageVertical)
                .frame(minHeight: Layout.userMessageMinimumHeight, alignment: .leading)
        }
        .background(Theme.messageSurface, in: .rect(cornerRadius: CornerRadius.card))
    }
}

struct AssistantBubble: View {
    let message: AssistantMessage
    @Bindable var project: ProjectSession
    @Bindable var run: RunSession
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        VStack(alignment: .leading, spacing: Spacing.standard) {
            ForEach(Array(MarkdownBlock.parse(message.text).enumerated()), id: \.offset) {
                _, block in
                switch block {
                case .paragraph(let body):
                    Text(.init(body)).textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                case .heading(let level, let text):
                    Text(.init(text))
                        .font(MarkdownBlock.headingFont(level)).fontWeight(.semibold)
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                case .unorderedListItem(let text):
                    MarkdownListRow(marker: "•", text: text)
                case .orderedListItem(let number, let text):
                    MarkdownListRow(marker: "\(number).", text: text)
                case .quote(let text):
                    MarkdownQuoteRow(text: text)
                case .rule:
                    Divider()
                case .code(let code, let language):
                    CodeBlock(code: code, language: language)
                }
            }
            ZStack(alignment: .leading) {
                ProgressView()
                    .controlSize(.small)
                    .frame(
                        width: Layout.inlineActivitySlot, height: Layout.inlineActivitySlot,
                        alignment: .leading
                    )
                    .opacity(message.isStreaming ? StateOpacity.visible : StateOpacity.hidden)
                // The full action row, not just copy: the slot reserves height
                // so the swap costs no layout, and the row sizes to its own
                // width because it is four controls wide, not one.
                MessageActions(message: message.text, project: project, run: run)
                    .frame(height: Layout.inlineActivitySlot, alignment: .leading)
                    .opacity(message.isStreaming ? StateOpacity.hidden : StateOpacity.visible)
            }
            // Both controls stay mounted in the same slot, so completing a
            // streamed reply changes only opacity instead of moving the text
            // that follows it in a long transcript.
            .animation(
                reduceMotion ? nil : .easeInOut(duration: Motion.loadingFadeDuration),
                value: message.isStreaming)
        }
        // Set once on the container rather than on each block. `.font` is an
        // environment value, so every Text below inherits it while headings
        // and code blocks still override with their own role. Without this the
        // blocks inherited the macOS 13pt system default — the transcript was
        // rendering three points under the type scale, and raising the scale
        // did nothing because nothing in the transcript ever read from it.
        .font(Typography.body)
        .lineSpacing(Typography.bodyLineSpacing)
    }
}

/// Each action maps to a real conversation operation; feedback buttons are
/// deliberately absent because this app has no feedback destination.
struct MessageActions: View {
    let message: String
    @Bindable var project: ProjectSession
    @Bindable var run: RunSession

    var body: some View {
        HStack(spacing: Spacing.messageActionPitch) {
            CopyMessageButton(text: message)
            CopyConversationButton(items: run.transcript.items)
            Button {
                Task { await project.fork() }
            } label: {
                Image(systemName: "arrow.triangle.branch")
            }
            .disabled(run.conversationID == nil)
            .help("Fork conversation")
            .accessibilityLabel("Fork conversation")
            Button {
                Task { await project.undo() }
            } label: {
                Image(systemName: "arrow.uturn.backward")
            }
            .disabled(run.conversationID == nil)
            .help("Undo last turn")
            .accessibilityLabel("Undo last turn")
        }
        // One size and one ink across all four. Different SF Symbols draw to
        // different heights at the same point size, so the row measured a 62%
        // spread; a fixed frame makes them a set. The colour is set once here
        // and the buttons no longer override it — three of four had been
        // inheriting the section-label rung instead.
        .font(.system(size: IconSize.detail))
        .symbolRenderingMode(.monochrome)
        .foregroundStyle(Theme.foregroundSubtle)
        .buttonStyle(.plain)
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// `Text(.init:)` only renders *inline* markdown (bold, italic, links, inline
/// code): it has no concept of block structure, so headings/lists/quotes came
/// out as literal `#`/`-`/`>` characters with no line breaks between them.
/// This splits a reply into block-level pieces so each one can pick its own
/// view, while still handing inline markdown for the block's own text back to
/// `Text(.init:)`.
///
/// Tables are out of scope — no attempt is made to detect `|` rows.
/// Nested lists are out of scope too: every list item is flat regardless of
/// leading indentation, to keep this parser (which reruns on every streamed
/// token) cheap and simple.
enum MarkdownBlock: Equatable {
    case paragraph(String)
    case heading(level: Int, text: String)
    case unorderedListItem(String)
    case orderedListItem(number: Int, text: String)
    case quote(String)
    case rule
    case code(String, String?)

    static func headingFont(_ level: Int) -> Font {
        switch level {
        case 1: return .title
        case 2: return .title2
        case 3: return .title3
        case 4: return .headline
        case 5: return .subheadline
        default: return .footnote
        }
    }

    static func parse(_ markdown: String) -> [MarkdownBlock] {
        var blocks: [MarkdownBlock] = []
        var paragraph: [String] = []
        var code: [String] = []
        var language: String?
        var inFence = false

        // A blank line or the start of any other block ends the paragraph
        // that was accumulating; a bare run of plain lines does not.
        func flushParagraph() {
            guard !paragraph.isEmpty else { return }
            // Two trailing spaces is CommonMark's hard line break. Joining
            // with a bare "\n" reads fine here but `Text(.init:)` reflows a
            // soft newline away, which is exactly the collapsing this exists
            // to avoid.
            blocks.append(.paragraph(paragraph.joined(separator: "  \n")))
            paragraph = []
        }

        for line in markdown.components(separatedBy: .newlines) {
            if line.hasPrefix("```") {
                if inFence {
                    blocks.append(.code(code.joined(separator: "\n"), language))
                    code = []
                    language = nil
                    inFence = false
                } else {
                    flushParagraph()
                    let tag = line.dropFirst(3).trimmingCharacters(in: .whitespaces)
                    language = tag.isEmpty ? nil : tag
                    inFence = true
                }
                continue
            }
            if inFence {
                code.append(line)
                continue
            }

            if let block = heading(line) {
                flushParagraph()
                blocks.append(block)
            } else if let block = unorderedItem(line) {
                flushParagraph()
                blocks.append(block)
            } else if let block = orderedItem(line) {
                flushParagraph()
                blocks.append(block)
            } else if let block = blockQuote(line) {
                flushParagraph()
                blocks.append(block)
            } else if isRule(line) {
                flushParagraph()
                blocks.append(.rule)
            } else if line.trimmingCharacters(in: .whitespaces).isEmpty {
                flushParagraph()
            } else {
                paragraph.append(line)
            }
        }

        // An unterminated fence is normal mid-stream: show it as code anyway.
        if inFence, !code.isEmpty { blocks.append(.code(code.joined(separator: "\n"), language)) }
        flushParagraph()
        return blocks
    }

    private static func heading(_ line: String) -> MarkdownBlock? {
        let level = line.prefix(while: { $0 == "#" }).count
        guard (1...6).contains(level) else { return nil }
        let rest = line.dropFirst(level)
        guard rest.hasPrefix(" ") else { return nil }
        return .heading(level: level, text: rest.trimmingCharacters(in: .whitespaces))
    }

    private static func unorderedItem(_ line: String) -> MarkdownBlock? {
        guard let marker = line.first, "-*+".contains(marker) else { return nil }
        let rest = line.dropFirst()
        guard rest.hasPrefix(" ") else { return nil }
        return .unorderedListItem(rest.trimmingCharacters(in: .whitespaces))
    }

    private static func orderedItem(_ line: String) -> MarkdownBlock? {
        let digits = line.prefix(while: \.isNumber)
        guard !digits.isEmpty, let number = Int(digits) else { return nil }
        let rest = line.dropFirst(digits.count)
        guard rest.hasPrefix(". ") else { return nil }
        return .orderedListItem(
            number: number, text: rest.dropFirst(2).trimmingCharacters(in: .whitespaces))
    }

    /// Named apart from the `quote` case itself: a same-named helper with a
    /// matching single-`String` argument shape resolved ambiguously against
    /// the case's own initializer and silently returned the wrong thing.
    private static func blockQuote(_ line: String) -> MarkdownBlock? {
        guard line.hasPrefix(">") else { return nil }
        return .quote(line.dropFirst().trimmingCharacters(in: .whitespaces))
    }

    /// `---`, `***`, `___` (3+ of the same character, nothing else on the
    /// line) is CommonMark's thematic break. Checked before the list-item
    /// tests below would even matter: none of them accept a bare `-`/`*` run
    /// with no following space, so there is no real ambiguity to resolve.
    private static func isRule(_ line: String) -> Bool {
        let trimmed = line.trimmingCharacters(in: .whitespaces)
        guard let marker = trimmed.first, "-*_".contains(marker) else { return false }
        return trimmed.count >= 3 && trimmed.allSatisfy { $0 == marker }
    }
}

/// Hanging indent for list markers: the marker sits in its own fixed-width
/// column so a wrapped second line lands under the text, not under the
/// bullet/number.
struct MarkdownListRow: View {
    let marker: String
    let text: String
    var body: some View {
        HStack(alignment: .top, spacing: Spacing.small) {
            Text(marker).foregroundStyle(Theme.foregroundTertiary).frame(
                minWidth: 18, alignment: .trailing)
            Text(.init(text)).textSelection(.enabled)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

struct MarkdownQuoteRow: View {
    let text: String
    var body: some View {
        HStack(spacing: Spacing.standard) {
            Rectangle().fill(Theme.foregroundQuaternary).frame(width: IconSize.rule)
            Text(.init(text)).foregroundStyle(Theme.foregroundTertiary).textSelection(.enabled)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

struct CodeBlock: View {
    let code: String
    let language: String?
    @State private var copied = false

    var body: some View {
        VStack(alignment: .leading, spacing: Spacing.none) {
            HStack {
                Text(language ?? "code").font(Typography.detail).foregroundStyle(
                    Theme.foregroundTertiary)
                Spacer()
                Button(copied ? "Copied" : "Copy") {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(code, forType: .string)
                    copied = true
                }
                .buttonStyle(.plain).font(Typography.detail).foregroundStyle(
                    Theme.foregroundTertiary)
            }
            .padding(.horizontal, Spacing.comfortable).padding(.vertical, 5)

            ScrollView(.horizontal) {
                Text(code)
                    .font(Typography.code).textSelection(.enabled)
                    .padding(.horizontal, Spacing.comfortable).padding(.bottom, 9)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
        .compactElevatedSurface()
    }
}

struct ThinkingRow: View {
    let text: String
    @State private var expanded = false
    var body: some View {
        DisclosureGroup("Thinking", isExpanded: $expanded) {
            Text(text)
                .font(Typography.code).foregroundStyle(Theme.foregroundTertiary)
                .textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .font(Typography.caption).foregroundStyle(Theme.foregroundTertiary)
    }
}

struct ToolRow: View {
    let activities: [ToolActivity]
    let onSelect: () -> Void

    var body: some View {
        Button(action: onSelect) {
            VStack(alignment: .leading, spacing: Spacing.small) {
                HStack(spacing: Spacing.tight) {
                    Text(TranscriptActivityLabel.text(for: activities))
                        .font(Typography.caption)
                        .foregroundStyle(Theme.foregroundTertiary)
                    Text("›")
                        .font(Typography.caption)
                        .foregroundStyle(Theme.foregroundQuaternary)
                    Spacer(minLength: Spacing.none)
                }
                Rectangle()
                    .fill(Theme.separator)
                    .frame(height: Spacing.toolRuleWeight)
            }
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityLabel(TranscriptActivityLabel.text(for: activities))
    }
}

/// Tool details live in the optional inspector; the transcript deliberately
/// summarizes work as one neutral line so it keeps the conversation's rhythm.
enum TranscriptActivityLabel {
    static func text(for activities: [ToolActivity]) -> String {
        guard let last = activities.last else { return "Worked" }
        guard activities.allSatisfy({ $0.status == .completed }) else { return text(for: last) }
        return completed(durationMS: activities.compactMap(\.durationMS).reduce(0, +))
    }

    static func text(for activity: ToolActivity) -> String {
        switch activity.status {
        case .completed: return completed(durationMS: activity.durationMS)
        case .running: return "Working"
        case .failed: return "Tool failed"
        case .blocked: return "Waiting for approval"
        }
    }

    static func completed(durationMS: Int?) -> String {
        guard let durationMS, durationMS > 0 else { return "Worked" }
        let seconds = Int((Double(durationMS) / 1_000).rounded(.up))
        return "Worked for \(seconds)s"
    }
}

/// Turns raw tool arguments into something scannable: `edit {"path":"a/b.swift"}`
/// reads as `a/b.swift`.
enum ToolSummary {
    static func describe(_ activity: ToolActivity) -> String {
        guard let data = activity.arguments.data(using: .utf8),
            let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
        else { return activity.arguments }

        for key in ["path", "file_path", "command", "pattern", "query", "url"] {
            if let value = object[key] as? String, !value.isEmpty { return value }
        }
        return object.keys.sorted().joined(separator: ", ")
    }
}

struct ErrorRow: View {
    let message: String
    var body: some View {
        HStack(alignment: .top, spacing: Spacing.standard) {
            Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.red)
            VStack(alignment: .leading, spacing: Spacing.compact) {
                Text(message).textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                // An error is the message most likely to be pasted elsewhere.
                CopyMessageButton(text: message)
                    .frame(maxWidth: .infinity, alignment: .trailing)
            }
        }
        .padding(Spacing.comfortable)
        .background(
            Color.red.opacity(StateOpacity.feedback), in: .rect(cornerRadius: CornerRadius.control))
    }
}

/// Server warnings that change what ran — most often the model being served by
/// a different provider than the one picked. Styled apart from errors: the run
/// still proceeds.
struct NoticeRow: View {
    let message: String
    var body: some View {
        HStack(alignment: .top, spacing: Spacing.standard) {
            Image(systemName: "info.circle.fill").foregroundStyle(.orange)
            Text(message).font(Typography.body).textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(Spacing.comfortable)
        .background(
            Color.orange.opacity(StateOpacity.feedback),
            in: .rect(cornerRadius: CornerRadius.control))
    }
}

// MARK: - Status, approvals, questions

/// Run state belongs to the transcript while work is active; a permanent
/// footer makes a quiet conversation look like a log viewer.
struct InlineRunStatus: View {
    @Bindable var run: RunSession
    let statusMessage: String?
    let scheduledRunStatus: String?
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        // Capture the identity this status row rendered for. SwiftUI can keep
        // this closure alive after a scheduled continuation selects a newer
        // run; resolving currentRunID in the action would then stop B.
        let renderedRunID = run.currentRunID
        MetadataRow(spacing: Spacing.standard) {
            ProgressView()
                .controlSize(.small)
                .frame(width: Layout.inlineActivitySlot, height: Layout.inlineActivitySlot)
                .opacity(run.isBusy ? StateOpacity.visible : StateOpacity.hidden)
                .animation(
                    reduceMotion ? nil : .easeInOut(duration: Motion.loadingFadeDuration),
                    value: run.isBusy)
            Text(label).foregroundStyle(Theme.foregroundSecondary)
            if let statusMessage {
                Text("· \(statusMessage)")
                    .foregroundStyle(Theme.foregroundQuaternary)
                    .lineLimit(1)
            }
            if let scheduledRunStatus {
                Text("· \(scheduledRunStatus)")
                    .foregroundStyle(Theme.foregroundQuaternary)
                    .lineLimit(1)
                    .accessibilityLabel(scheduledRunStatus)
            }
            Spacer()
            if run.isBusy, let renderedRunID {
                Button(run.cancelInFlight ? "Stopping…" : "Stop") {
                    run.cancel(expectedRunID: renderedRunID)
                }
                .controlSize(.small)
                .disabled(run.cancelInFlight)
            }
        }
        .onChange(of: run.connectionError) { _, error in
            guard let error, let application = NSApp else { return }
            NSAccessibility.post(
                element: application,
                notification: .announcementRequested,
                userInfo: [.announcement: error, .priority: NSAccessibilityPriorityLevel.high])
        }
        .accessibilityElement(children: .combine)
    }

    private var label: String {
        if let error = run.connectionError { return error }
        if run.cancelInFlight { return "Stopping…" }
        switch run.transcript.runState {
        case .idle: return run.planMode ? "Plan mode — ready" : "Ready"
        case .queued: return "Starting"
        case .running: return "Working"
        case .waitingForUser: return "Waiting for you"
        case .cancelling: return "Stopping — press Stop again to force"
        case .completed: return "Done"
        case .failed: return "Failed"
        case .cancelled: return "Cancelled"
        }
    }
}

/// Flattens a transcript to plain text for the clipboard, so the whole
/// conversation can be pasted into an issue or a message.
enum TranscriptText {
    static func plain(_ items: [TranscriptItem]) -> String {
        items.map(line).joined(separator: "\n\n")
    }

    private static func line(_ item: TranscriptItem) -> String {
        switch item.kind {
        case .userPrompt(let text): return "You: \(text)"
        case .assistantMessage(let message): return message.text
        case .thinking(let text): return "Thinking: \(text)"
        case .toolActivity(let activity):
            return "[\(activity.tool)] \(ToolSummary.describe(activity))"
        case .error(let message): return "Error: \(message)"
        case .notice(let message): return "Note: \(message)"
        case .compaction(let summary, let removed):
            return "History compacted (\(removed) messages folded): \(summary)"
        }
    }
}

struct UsageLabel: View {
    let usage: UsageTotals
    var body: some View {
        if usage.totalTokens > 0 {
            // An unpriced model reports 0; "$0.00" would read as free.
            Text(
                usage.costIsKnown
                    ? "\(usage.totalTokens) tok · $\(String(format: "%.4f", usage.costUSD))"
                    : "\(usage.totalTokens) tok · cost n/a"
            )
            // Body size, separated by colour rather than by scale. The reference's
            // "Worked for 11s" sits 0.5pt below its own body ascender; ours sat
            // 2.5pt below, de-scaled *and* dimmed where the reference only dims.
            .font(Typography.body).foregroundStyle(Theme.foregroundTertiary)
        }
    }
}

/// Approvals are the highest-stakes interaction here, so Deny is the plain
/// action and nothing is bound to Return.
struct ApprovalBar: View {
    let approval: PendingApproval
    @Bindable var run: RunSession
    @State private var showArguments = false

    var body: some View {
        VStack(alignment: .leading, spacing: Spacing.standard) {
            HStack(spacing: Spacing.standard) {
                Image(systemName: "hand.raised.fill").foregroundStyle(.orange)
                Text("Allow **\(approval.tool)** to run?")
                Spacer()
                Button(showArguments ? "Hide" : "Details") { showArguments.toggle() }
                    .buttonStyle(.plain).font(Typography.caption).foregroundStyle(
                        Theme.foregroundTertiary)
                Button("Deny") { run.deny(expectedRunID: approval.runID) }.disabled(
                    run.runControlInFlight)
                Button("Allow") { run.approve(expectedRunID: approval.runID) }.buttonStyle(
                    .borderedProminent
                )
                .disabled(run.runControlInFlight)
            }
            if showArguments {
                ScrollView {
                    Text(approval.arguments)
                        .font(Typography.codeCaption).textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
                .frame(maxHeight: 120)
                .padding(Spacing.standard)
                .background(Theme.surfaceElevated, in: .rect(cornerRadius: CornerRadius.code))
            }
        }
        // Same 16pt left inset as the transcript column and the status bar.
        .padding(.horizontal, Spacing.large).padding(.vertical, 9)
        .background(Color.orange.opacity(StateOpacity.emphasis))
    }
}

// MARK: - Composer

struct Composer: View {
    @Bindable var project: ProjectSession
    @Bindable var run: RunSession
    @FocusState private var focused: Bool
    @State private var mentions: [FileCompletion.Match] = []
    @State private var mentionTask: Task<Void, Never>?

    var body: some View {
        // The same composer can remain on screen while a continuation replaces
        // its active run. Send must retain the run it rendered as steering.
        let action = ComposerAction.capture(canSteer: run.canSteer, runID: run.currentRunID)
        VStack(alignment: .leading, spacing: Spacing.standard) {
            if !mentions.isEmpty {
                MentionPopup(matches: mentions) { match in
                    run.draft = MentionQuery.replacing(run.draft, with: match.relativePath)
                    mentions = []
                }
            }

            // One elevated card holds every composer control (baseline gap
            // #2): the field, send button, model picker and plan toggle used
            // to read as three unrelated regions — the field, a right-hand
            // gutter for send, and a separate borderless strip below.
            ConversationColumn {
                VStack(alignment: .leading, spacing: Spacing.comfortable) {
                    TextField(placeholder, text: $run.draft, axis: .vertical)
                        // Without this the field inherits the macOS system
                        // default and the placeholder renders a full step
                        // under the reference's.
                        .font(Typography.body)
                        .textFieldStyle(.plain)
                        .lineLimit(1...10)
                        .focused($focused)
                        .onSubmit { send(action) }
                        .onChange(of: run.draft) { _, text in updateMentions(for: text) }

                    HStack(spacing: Spacing.comfortable) {
                        ModelChip(project: project)
                        // A chip, not a checkbox. The reference's composer
                        // uses icon+label chips and contains no checkbox
                        // anywhere; a square AppKit control in a chat composer
                        // was the most out-of-family element on the screen.
                        ComposerChip(
                            title: "Plan mode",
                            icon: "list.bullet.rectangle",
                            isOn: project.planMode
                        ) { project.planMode.toggle() }
                        .help("Restrict the agent to writing a plan file until you approve it")
                        Spacer()
                        Button("New") { project.newConversation() }
                            .buttonStyle(.plain).font(Typography.body).foregroundStyle(
                                Theme.foregroundTertiary)

                        Button(action: { send(action) }) {
                            Image(
                                systemName: run.canSteer
                                    ? "arrow.turn.up.right" : "arrow.up.circle.fill"
                            )
                            // A title-sized SF Symbol makes its filled disc half
                            // the target control; this restores the send target's
                            // intended visual weight without changing its glyph.
                            .font(.system(size: 34))
                        }
                        .buttonStyle(.plain)
                        .disabled(run.draft.trimmed.isEmpty || run.runControlInFlight)
                        .help(action.isSteer ? "Steer the running task" : "Send")
                        .accessibilityLabel(
                            action.isSteer ? "Steer the running task" : "Send message")
                    }
                }
                // The reference insets its composer controls 9pt from the
                // right edge and 9.5 from the bottom; ours sat at 18.5 and 20,
                // twice as far in, inside a shorter composer.
                .padding(.horizontal, Spacing.comfortable).padding(.vertical, Spacing.inset)
                .background(Theme.surfaceElevated, in: .rect(cornerRadius: CornerRadius.composer))
                // The reference's composer carries a hairline on all four
                // edges; ours sat as flat fill straight against the page.
                // Theme.rule, not Theme.separator: at 43 against a 45 fill the
                // stroke was darker than what it bordered and indistinguishable
                // from the shape's own antialiased edge. A border has to be
                // lighter than its fill to read as one.
                .overlay(
                    RoundedRectangle(cornerRadius: CornerRadius.composer, style: .continuous)
                        .strokeBorder(Theme.rule, lineWidth: Spacing.hairline)
                )
            }
            // No minimum height: the card hugs its content and grows with a
            // multi-line draft. A fixed floor was measured against the old,
            // smaller type scale — once body type grew to the Codex 16.5pt the
            // floor still exceeded the content, and the surplus rendered as
            // dead space padding out the bottom of the card.
        }
        // Equal above and below. These were 8pt and 18pt, so the card sat
        // visibly high in its own gutter — the inset that makes it read as
        // chrome rather than a footer only works when it is symmetric.
        .padding(.vertical, Spacing.section)
        .onAppear { focused = true }
    }

    /// Debounced and cancellable: a large repo has hundreds of thousands of
    /// files and the composer must stay responsive while typing.
    private func updateMentions(for text: String) {
        mentionTask?.cancel()
        guard let query = MentionQuery.current(in: text) else {
            mentions = []
            return
        }
        let completion = FileCompletion(roots: [project.workspace])
        mentionTask = Task {
            try? await Task.sleep(for: .milliseconds(120))
            guard !Task.isCancelled else { return }
            let found = await completion.matches(for: query, limit: 8)
            guard !Task.isCancelled else { return }
            mentions = found
        }
    }

    private var placeholder: String {
        run.canSteer ? "Steer the running task…" : "Ask the harness to do something…"
    }

    /// While a run is active the same control steers instead of queueing a
    /// second run, matching the TUI's mid-turn steering.
    private func send(_ action: ComposerAction) {
        action.perform(
            canSubmit: run.canSubmit,
            steer: { run.steer(expectedRunID: $0) },
            submit: { project.submit() })
    }
}

/// Captured once for a rendered composer interaction. In particular, a Send
/// closure rendered as A's steer action must remain a steer request for A if a
/// scheduled B replaces it before the click/Return handler executes.
enum ComposerAction: Equatable {
    case submit
    case steer(String)

    static func capture(canSteer: Bool, runID: String?) -> Self {
        if canSteer, let runID { return .steer(runID) }
        return .submit
    }

    var isSteer: Bool {
        if case .steer = self { return true }
        return false
    }

    /// Keeps the rendered branch immutable through a delayed button/Return
    /// callback. This is intentionally a tiny pure seam so ownership can be
    /// regression-tested without a SwiftUI view host.
    func perform(
        canSubmit: Bool, steer: (String) -> Void, submit: () -> Void
    ) {
        switch self {
        case .steer(let runID):
            steer(runID)
        case .submit:
            guard canSubmit else { return }
            submit()
        }
    }
}

/// An icon+label chip for a composer toggle.
///
/// The reference's composer expresses its options this way; a square AppKit
/// checkbox in a chat composer was the most out-of-family control on the
/// screen. Selection reads through ink and a fill rather than a tick, so the
/// control keeps the composer's vocabulary instead of importing a form's.
struct ComposerChip: View {
    let title: String
    let icon: String
    let isOn: Bool
    let toggle: () -> Void

    var body: some View {
        Button(action: toggle) {
            // Explicit icon+label rather than Label: Label sizes its symbol
            // from the font's own metrics, which made this chip's icon 48%
            // wider than the model chip's beside it and its label gap 85%
            // larger. Fixing both to tokens puts the two chips on one size.
            HStack(spacing: Spacing.chipLabelGap) {
                Image(systemName: icon)
                    .font(.system(size: IconSize.chip))
                    .frame(width: IconSize.chip, height: IconSize.chip)
                Text(title)
            }
            .font(Typography.body)
            .foregroundStyle(isOn ? Theme.foreground : Theme.foregroundTertiary)
            .padding(.horizontal, Spacing.small)
            .padding(.vertical, Spacing.tight)
            .background(
                isOn ? Theme.selectedRowSurface : .clear,
                in: .rect(cornerRadius: CornerRadius.control)
            )
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(isOn ? [.isSelected] : [])
    }
}

struct ModelChip: View {
    @Bindable var project: ProjectSession

    var body: some View {
        Menu {
            Button("Server default") { project.selectedModel = nil }
            Divider()
            ForEach(usableProviders, id: \.self) { provider in
                Menu(provider) {
                    ForEach(models(for: provider)) { model in
                        Button {
                            project.selectedModel = model.id
                        } label: {
                            // The TUI picker shows neither; both drive the choice.
                            Text(model.priceSummary.map { "\(model.id) — \($0)" } ?? model.id)
                        }
                    }
                }
            }
            // Hiding models without saying so reads as "my model disappeared".
            // One disabled line names the count and the reason.
            if !hiddenSummary.isEmpty {
                Divider()
                Text(hiddenSummary).font(Typography.caption)
            }
        } label: {
            // Same construction as ComposerChip so the two sit as a set:
            // one icon size, one label gap, one baseline.
            HStack(spacing: Spacing.chipLabelGap) {
                Image(systemName: "cpu")
                    .font(.system(size: IconSize.chip))
                    .frame(width: IconSize.chip, height: IconSize.chip)
                Text(project.selectedModel ?? "Server default")
            }
            .font(Typography.body)
        }
        .menuStyle(.borderlessButton)
        .fixedSize()
    }

    /// Provider names that can actually run a model, or nil when the provider
    /// list is unavailable.
    ///
    /// Failing open matters here: `refreshCatalog` swallows a failed fetch into
    /// an empty array, and filtering against that would hide every model and
    /// leave a picker containing nothing but "Server default". An over-full
    /// menu is a worse menu; an empty one looks like the app is broken.
    private var usableProviderNames: Set<String>? {
        guard !project.providers.isEmpty else { return nil }
        return Set(project.providers.filter(\.isUsable).map(\.name))
    }

    /// Only providers whose credentials exist and are not known-broken. A model
    /// the user cannot run has no business being offered: picking it fails the
    /// run, and the failure names a provider rather than the model they chose.
    private var usableProviders: [String] {
        let all = Set(project.models.map(\.provider))
        guard let usable = usableProviderNames else { return all.sorted() }
        return Array(all.intersection(usable)).sorted()
    }

    private func models(for provider: String) -> [ModelInfo] {
        project.models.filter { $0.provider == provider }.sorted { $0.id < $1.id }
    }

    private var hiddenSummary: String {
        guard let usable = usableProviderNames else { return "" }
        let hidden = project.models.filter { !usable.contains($0.provider) }
        guard !hidden.isEmpty else { return "" }

        let broken = project.providers
            .filter { $0.configured && $0.health == "failed" }
            .map(\.name).sorted()
        if !broken.isEmpty {
            return "\(hidden.count) models hidden — \(broken.joined(separator: ", ")) "
                + "\(broken.count == 1 ? "needs" : "need") re-authentication"
        }
        return "\(hidden.count) models hidden — no credentials for their providers"
    }
}

struct LabelledCode: View {
    let title: String
    let content: String

    init(title: String, body: String) {
        self.title = title
        self.content = body
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            Text(title).font(Typography.caption.weight(.semibold)).foregroundStyle(
                Theme.foregroundQuaternary)
            ScrollView(.horizontal) {
                Text(content)
                    .font(Typography.code).textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .padding(Spacing.comfortable)
            .compactElevatedSurface()
        }
    }
}

/// File suggestions for an in-progress `@mention`.
struct MentionPopup: View {
    let matches: [FileCompletion.Match]
    let onPick: (FileCompletion.Match) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: Spacing.none) {
            ForEach(matches) { match in
                Button {
                    onPick(match)
                } label: {
                    HStack(spacing: Spacing.small) {
                        Image(systemName: "doc").font(Typography.detail).foregroundStyle(
                            Theme.foregroundTertiary)
                        Text(match.relativePath)
                            .font(Typography.codeCaption)
                            .lineLimit(1).truncationMode(.head)
                        Spacer()
                    }
                    .padding(.horizontal, Spacing.comfortable).padding(.vertical, 5)
                    .contentShape(.rect)
                }
                .buttonStyle(.plain)
            }
        }
        .compactElevatedSurface()
    }
}

/// Leaving plan mode: show the plan, then approve with a chosen approach.
struct PlanApprovalView: View {
    let plan: PendingPlan
    @Bindable var run: RunSession
    @State private var selected: String?

    var body: some View {
        VStack(alignment: .leading, spacing: Spacing.comfortable) {
            Label("Ready to leave plan mode", systemImage: "list.bullet.clipboard")
                .font(Typography.body.weight(.medium))

            ScrollView {
                Text(.init(plan.plan))
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .frame(maxHeight: 220)
            .padding(Spacing.comfortable)
            .compactElevatedSurface()

            if !plan.options.isEmpty {
                Text("Approach").font(Typography.caption).foregroundStyle(
                    Theme.foregroundQuaternary)
                ForEach(plan.options) { option in
                    Button {
                        selected = option.id
                    } label: {
                        HStack(alignment: .top, spacing: 7) {
                            Image(
                                systemName: selected == option.id
                                    ? "largecircle.fill.circle" : "circle")
                            VStack(alignment: .leading, spacing: Spacing.hairline) {
                                Text(option.label)
                                if let detail = option.description, !detail.isEmpty {
                                    Text(detail).font(Typography.caption).foregroundStyle(
                                        Theme.foregroundTertiary)
                                }
                            }
                            Spacer()
                        }
                    }
                    .buttonStyle(.plain)
                }
            }

            HStack {
                Spacer()
                Button("Keep Planning") { run.deny(expectedRunID: plan.runID) }.disabled(
                    run.runControlInFlight)
                Button("Approve") { run.approve(expectedRunID: plan.runID, option: selected) }
                    .buttonStyle(.borderedProminent)
                    // With approaches offered, one must be chosen.
                    .disabled((!plan.options.isEmpty && selected == nil) || run.runControlInFlight)
            }
        }
        // Same 16pt left inset as the transcript column and the status bar.
        .padding(.horizontal, Spacing.large).padding(.vertical, 14)
        .background(Theme.accent.opacity(StateOpacity.subtle))
    }
}
