import AppKit
import HarnessKit
import SwiftUI

public enum Section: String, CaseIterable, Identifiable {
    case chat, activity, sessions, checkpoints, settings

    public var id: String { rawValue }

    var title: String {
        switch self {
        case .chat: return "Chat"
        case .activity: return "Activity"
        case .sessions: return "Sessions"
        case .checkpoints: return "Checkpoints"
        case .settings: return "Settings"
        }
    }

    var icon: String {
        switch self {
        case .chat: return "bubble.left.and.text.bubble.right"
        case .activity: return "chart.bar.doc.horizontal"
        case .sessions: return "clock.arrow.circlepath"
        case .checkpoints: return "arrow.uturn.backward.circle"
        case .settings: return "gearshape"
        }
    }
}

/// Root view. Shows a project picker until a workspace is chosen, because
/// harnessd cannot start without one — its workspace is fixed at process launch.
public struct AppShell: View {
    @State private var project: ProjectSession?
    @State private var section: Section = .chat
    private let initialPrompt: String?

    public init(
        initialWorkspace: URL? = nil, externalBaseURL: URL? = nil, initialPrompt: String? = nil
    ) {
        self.initialPrompt = initialPrompt
        if let initialWorkspace {
            _project = State(
                initialValue: ProjectSession(
                    workspace: initialWorkspace, externalBaseURL: externalBaseURL))
        }
    }

    public var body: some View {
        Group {
            if let project {
                ProjectView(project: project, section: $section, initialPrompt: initialPrompt) {
                    Task {
                        await project.shutdown()
                        self.project = nil
                    }
                }
            } else {
                ProjectPicker { url in
                    project = ProjectSession(workspace: url)
                }
            }
        }
        // Widened for the labeled rail (was 50pt icon-only, now 220pt) plus
        // room for the inspector pane to open without squeezing the
        // transcript below its own minWidth.
        .frame(minWidth: Layout.appMinimumWidth, minHeight: Layout.appMinimumHeight)
        // The root surface. Every other token in `Theme` is defined relative
        // to this value, so it has to be painted explicitly rather than left
        // as the system window background — that's the one substitution
        // that pulled every surface in the app toward the same narrow, warm
        // range (`.ux/design-baseline.md` §8).
        .background(Theme.background)
    }
}

private struct ProjectPicker: View {
    let onOpen: (URL) -> Void

    var body: some View {
        VStack(spacing: Spacing.section) {
            // The app's own mark rather than a borrowed SF Symbol — this is
            // the first screen a new user sees, and it is the one place the
            // product gets to say what it is.
            BrandMarkView(side: IconSize.launch)
                .foregroundStyle(Theme.foregroundSecondary)
            VStack(spacing: Spacing.small) {
                Text("Open a project").font(Typography.title.weight(.semibold))
                Text("Each project runs its own harness server.")
                    .font(Typography.body).foregroundStyle(Theme.foregroundTertiary)
            }
            Button("Choose Folder…", action: choose)
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private func choose() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.allowsMultipleSelection = false
        panel.prompt = "Open"
        if panel.runModal() == .OK, let url = panel.url {
            onOpen(url)
        }
    }
}

private struct ProjectView: View {
    @Bindable var project: ProjectSession
    @Binding var section: Section
    var initialPrompt: String?
    let onClose: () -> Void

    var body: some View {
        HStack(spacing: Spacing.none) {
            ConversationRail(section: $section, project: project, onClose: onClose)
            // An explicit rule rather than Divider(): the system divider drew
            // 47 where the reference draws 67, the only one of the app's four
            // rules whose colour was wrong.
            Rectangle().fill(Theme.columnDivider).frame(width: Spacing.hairline)
            content
        }
        .task {
            await project.start()
            // Lets the app be driven from the command line, and is how the
            // streaming UI gets exercised without a human at the keyboard.
            if let initialPrompt, project.isReady, let run = project.run {
                run.draft = initialPrompt
                project.submit()
            }
        }
        // Hidden, not coloured. A visible toolbar background paints one colour
        // across the whole window width, so the sidebar's grey ran over the
        // content pane and produced a full-width band down to y=52 — the
        // content pane did not exist at that height. The reference has no such
        // band: each column paints its own colour to the top of the window, so
        // hiding the toolbar background lets the rail and the content pane do
        // exactly that. Both already extend through the top safe area.
        .toolbarBackground(.hidden, for: .windowToolbar)
    }

    @ViewBuilder
    private var content: some View {
        switch project.phase {
        case .idle, .starting:
            StartingView(workspace: project.workspace)
        case .failed(let message):
            StartupFailureView(message: message) { Task { await project.start() } }
        case .ready:
            switch section {
            case .chat:
                if let run = project.run {
                    ChatView(project: project, run: run)
                }
            case .activity:
                ActivityView(project: project, section: $section)
            case .sessions:
                SessionsView(project: project, section: $section)
            case .checkpoints:
                CheckpointsView(project: project)
            case .settings:
                SettingsView(project: project)
            }
        }
    }
}

/// Labeled, sectioned nav — was a 50pt icon-only strip with no labels and no
/// grouping (baseline gap #5). Sessions + Checkpoints are grouped under a
/// "History" heading per the design plan's A2, since both browse past state
/// rather than switching between live-run modes the way Chat/Activity do.
/// One nav row: icon + text label, so the accessibility name is the label
/// text rather than the SF Symbol identifier (baseline gap #5) and a sighted
/// user gets a name too, not just a glyph.
struct RailRow: View {
    @Binding var section: Section
    let item: Section
    var compact = false

    var body: some View {
        Button {
            section = item
        } label: {
            HStack(spacing: Spacing.comfortable) {
                Image(systemName: item.icon)
                    .font(.system(size: IconSize.standard))
                    .frame(width: IconSize.row)
                    // The label text alone names this row; without this the
                    // icon contributes its own SF Symbol identifier to the
                    // combined accessibility description.
                    .accessibilityHidden(true)
                if !compact {
                    Text(item.title).font(Typography.body)
                    Spacer(minLength: Spacing.none)
                }
            }
            // A compact row carries an icon and nothing else, so it takes the
            // tighter inset. At the labelled inset five of them needed 249pt
            // inside a 204pt column: the overflow widened the whole rail and
            // pushed its rows off-centre, which is why the selected pill sat
            // 1pt from the left edge and 15pt from the right.
            .padding(.horizontal, compact ? Spacing.small : Spacing.comfortable)
            .padding(.vertical, Spacing.standard)
            .frame(maxWidth: compact ? nil : .infinity, alignment: .leading)
            // No fill on nav rows. selectedRowSurface belongs to the active
            // conversation and nothing else — painting it here too put two
            // competing "this one is selected" affordances in the sidebar
            // 31.5pt apart, and the reference reserves that fill for the
            // conversation alone. Selection still reads, through ink weight.
            // No fill here. The fill is the *conversation* list's, and the
            // reference shows exactly one filled row in its whole sidebar.
            // Round 8 removed this and left no selection anywhere; round 10
            // restored it and produced two identical bands. A nav row is
            // current, not selected — weight and ink carry that without
            // competing with the conversation the user is actually in.
            .foregroundStyle(
                // Secondary, not primary: the reference's nav labels sit at
                // 222 selected or not, and spend 255 on content instead.
                section == item ? Theme.foregroundSecondary : Theme.foregroundSubtle
            )
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .help(item.title)
        .accessibilityLabel(item.title)
    }
}

struct RailSectionHeader: View {
    let title: String
    init(_ title: String) { self.title = title }

    var body: some View {
        Text(title)
            // Larger and dimmer than before: the reference's section label is
            // bigger than this was and two rungs darker, so it reads as a
            // heading that recedes. Ours was smaller and brighter, which is
            // the opposite relationship. foregroundQuaternary is #747474,
            // which is the reference's ink exactly.
            .font(Typography.caption)
            .foregroundStyle(Theme.foregroundQuaternary)
            .padding(.horizontal, Spacing.section).padding(.top, Spacing.comfortable)
            .padding(.bottom, Spacing.tight)
            // A heading, not a control — VoiceOver should announce it once
            // as a group label, not treat it as another focusable row.
            .accessibilityAddTraits(.isHeader)
    }
}

private struct StartingView: View {
    let workspace: URL
    var body: some View {
        VStack(spacing: Spacing.comfortable) {
            ProgressView()
            Text("Starting the harness for \(workspace.lastPathComponent)…")
                .font(Typography.body).foregroundStyle(Theme.foregroundTertiary)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

/// A startup failure is otherwise invisible — the app would just sit there — so
/// the child's own stderr is shown verbatim.
private struct StartupFailureView: View {
    let message: String
    let retry: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Label(
                "The harness server could not start", systemImage: "exclamationmark.triangle.fill"
            )
            .font(Typography.heading).foregroundStyle(.orange)
            ScrollView {
                Text(message)
                    .font(Typography.code).textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .frame(maxHeight: 260)
            .padding(Spacing.comfortable)
            .compactElevatedSurface()
            Button("Try Again", action: retry).buttonStyle(.borderedProminent)
        }
        .padding(Spacing.page)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
    }
}
