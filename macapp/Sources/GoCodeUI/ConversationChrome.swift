import HarnessKit
import SwiftUI

/// One layout envelope prevents the transcript and composer from drifting to
/// different horizontal origins as their internals evolve.
struct ConversationColumn<Content: View>: View {
    @ViewBuilder let content: Content

    var body: some View {
        content
            .frame(maxWidth: Layout.chatContentMaximumWidth)
            .frame(maxWidth: .infinity, alignment: .center)
    }
}

/// The conversation's identity, sized to sit on the window-control row.
///
/// Split out of ConversationHeader so the toolbar can carry it: as its own
/// band below the traffic lights it made the header 87% taller than the
/// reference's and truncated the title beside 402pt of empty row.
struct ConversationTitle: View {
    @Bindable var project: ProjectSession
    @Bindable var run: RunSession

    var body: some View {
        HStack(spacing: Spacing.small) {
            Image(systemName: "folder")
                .font(.system(size: IconSize.detail))
                .foregroundStyle(Theme.foreground)
                .accessibilityHidden(true)
            Text(title)
                .font(Typography.body.weight(.medium))
                .foregroundStyle(Theme.foreground)
                .lineLimit(1)
            Menu {
                Button("New conversation") { project.newConversation() }
                if run.conversationID != nil {
                    Button("Fork conversation") { Task { await project.fork() } }
                    Button("Undo last turn") { Task { await project.undo() } }
                }
            } label: {
                Image(systemName: "ellipsis")
                    .font(.system(size: IconSize.detail))
            }
            .menuStyle(.borderlessButton)
            // Subtle, and applied as a tint: .borderlessButton re-tints
            // whatever label it is given, so foregroundStyle here does nothing.
            .tint(Theme.foregroundSubtle)
            .fixedSize()
            .help("Conversation actions")
            .accessibilityLabel("Conversation actions")
        }
    }

    private var title: String {
        guard let id = run.conversationID,
            let conversation = project.conversations.first(where: { $0.id == id })
        else { return "New conversation" }
        return conversation.displayTitle
    }
}

struct ConversationHeader: View {
    @Bindable var project: ProjectSession
    @Bindable var run: RunSession
    /// Carried here rather than in a window toolbar. A toolbar forces a
    /// system titlebar band, and this header then had to sit *below* it —
    /// which is the whole reason the header measured 91.5pt against the
    /// reference's 55. Owning its own controls lets the band be the only one.
    @Binding var showInspector: Bool

    var body: some View {
        // Not ConversationColumn. Binding the header to the transcript's
        // column truncated the title at the column edge while 402pt of its own
        // row sat empty to the right. The reference gives its title the whole
        // header row and column-binds only the transcript.
        HStack(spacing: Spacing.small) {
            // Primary, not tertiary. The reference draws this icon at
            // full white — it belongs to the title beside it. Ours sat two
            // rungs down while the overflow menu next to it sat two rungs
            // up, so the row's hierarchy was inverted at both ends.
            Image(systemName: "folder")
                .font(.system(size: IconSize.detail))
                .foregroundStyle(Theme.foreground)
                .accessibilityHidden(true)
            Text(title)
                .font(Typography.body.weight(.medium))
                // Primary, matching the folder icon beside it. Setting the
                // icon to white and leaving the title a rung down made the
                // decoration brighter than the label it decorates.
                .foregroundStyle(Theme.foreground)
                .lineLimit(1)
            Spacer(minLength: Spacing.none)
            CopyConversationButton(items: run.transcript.items)
            Button {
                showInspector.toggle()
            } label: {
                Image(systemName: showInspector ? "sidebar.trailing" : "sidebar.right")
            }
            .buttonStyle(.plain)
            .foregroundStyle(Theme.foregroundSubtle)
            .help(showInspector ? "Hide tool inspector" : "Show tool inspector")
            .accessibilityLabel(showInspector ? "Hide tool inspector" : "Show tool inspector")
            Menu {
                Button("New conversation") { project.newConversation() }
                if run.conversationID != nil {
                    Button("Fork conversation") { Task { await project.fork() } }
                    Button("Undo last turn") { Task { await project.undo() } }
                }
            } label: {
                // Subtle: an overflow menu should be findable when looked
                // for and invisible otherwise. This was brighter than the
                // title's own folder icon.
                Image(systemName: "ellipsis")
                    .font(.system(size: IconSize.detail))
                    .foregroundStyle(Theme.foregroundSubtle)
            }
            .menuStyle(.borderlessButton)
            // Tint on the Menu, not on its label: .borderlessButton
            // re-tints the label it is given, so styling the Image alone
            // left the overflow brighter than the title's own folder icon.
            .tint(Theme.foregroundSubtle)
            // A .borderlessButton menu expands to fill whatever width it
            // is offered, which swallowed the Spacer and left the header's
            // controls clustered beside the title instead of at the right.
            .fixedSize()
            .help("Conversation actions")
            .accessibilityLabel("Conversation actions")
        }
        .frame(height: Spacing.conversationHeaderHeight)
        .padding(.horizontal, Spacing.section)
        // The reference is a compartmented app: rules cut header from
        // transcript, transcript from composer, sidebar from account. With
        // none of them the window reads as one flat plane with a floating box.
        .overlay(alignment: .bottom) {
            Rectangle()
                .fill(Theme.rule)
                .frame(height: Spacing.hairline)
        }
    }

    private var title: String {
        guard let id = run.conversationID,
            let conversation = project.conversations.first(where: { $0.id == id })
        else { return "New conversation" }
        return conversation.displayTitle
    }
}
