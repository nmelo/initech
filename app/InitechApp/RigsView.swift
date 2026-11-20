import InitechCore
import SwiftUI
import UIKit

/// RigsAccordion is the Rigs screen. Every rig's header is always on screen:
/// closed, one under another, at the top of the scroll. Scrolling opens the
/// first rig, moves through its panes, then closes it into its header as the
/// next one opens (RigStack is the arithmetic). A rig's panes are all listed,
/// pinned or not; sliding a row left pins or unpins it. Tapping a header goes
/// to that rig.
struct RigsAccordion: View {
    @ObservedObject var home: HomeModel
    /// besideThePane is true on iPad, where the list sits beside the open pane
    /// and the row whose pane is open is marked. On either device, tapping a
    /// row opens that pane's conversation, pinned or not.
    var besideThePane = false
    @ScaledMetric(relativeTo: .body) private var headerHeight: CGFloat = 46
    @ScaledMetric(relativeTo: .body) private var rowHeight: CGFloat = 62
    /// scroll is how far the screen is scrolled; viewport the height it shows.
    @State private var scroll: CGFloat = 0
    @State private var viewport: CGFloat = 0
    @State private var position = ScrollPosition(edge: .top)
    /// openRow is the one row slid open to show its key, if any.
    @State var openRow: PaneRef?

    var body: some View {
        let rigs = home.rigPanes
        let stack = RigStack(rows: rigs.map(\.panes.count), header: headerHeight, row: rowHeight, viewport: viewport)
        ScrollView {
            if rigs.isEmpty {
                Text("No rig is running on the Mac.").font(Theme.mono(.footnote)).foregroundStyle(Theme.dim)
                    .frame(maxWidth: .infinity).padding(.top, 40)
            } else if stack.fits {
                // The column is as tall as the scroll is long; what shows is laid out
                // from the offset and held in place over it.
                Color.clear.frame(height: viewport + stack.length)
                    .overlay(alignment: .top) {
                        stacked(rigs, stack.layout(at: scroll), stack)
                            .frame(height: viewport, alignment: .top)
                            // Held in place only between the two ends: pulled past the top or
                            // pushed past the bottom, it moves with the scroll and springs back,
                            // as a list does.
                            .offset(y: min(max(scroll, 0), stack.length))
                    }
            } else {
                // Too many rigs for their headers to share the screen: a plain list.
                VStack(spacing: 0) {
                    ForEach(Array(rigs.enumerated()), id: \.element.rig) { _, rig in
                        header(rig.rig, count: rig.panes.count, open: true) {}
                        rows(rig)
                    }
                }
            }
        }
        .scrollPosition($position)
        .onScrollGeometryChange(for: [CGFloat].self) { g in
            // containerSize is already what shows between the header and the bottom bar:
            // the insets are not part of it (measured: 699 of an 874-point screen, insets 106 and 68).
            [g.contentOffset.y + g.contentInsets.top, g.containerSize.height]
        } action: { old, now in
            // Scrolling closes a row left open, as a list does.
            if openRow != nil, abs(now[0] - old[0]) > 2 { withAnimation(.snappy) { openRow = nil } }
            scroll = now[0]
            viewport = max(0, now[1])
        }
        .refreshable { await home.reload() }
        #if DEBUG
        .onChange(of: viewport > 0 && !rigs.isEmpty) { _, ready in
            if ready, let y = UITestHooks.scroll { position.scrollTo(y: y) }
        }
        #endif
    }

    private func stacked(_ rigs: [HomeModel.RigPanes], _ layout: RigStack.Layout, _ stack: RigStack) -> some View {
        ZStack(alignment: .topLeading) {
            ForEach(Array(rigs.enumerated()), id: \.element.rig) { k, rig in
                rows(rig)
                    .offset(y: layout.shift[k])
                    .frame(height: layout.window[k], alignment: .top)
                    .clipped()
                    .allowsHitTesting(layout.window[k] > 1)
                    .accessibilityHidden(layout.window[k] <= 1)
                    .offset(y: layout.headerY[k] + headerHeight)
                header(rig.rig, count: rig.panes.count, open: layout.window[k] > 1) {
                    // Quick: a tap on a header should feel like a switch, however far it travels.
                    withAnimation(.easeOut(duration: 0.18)) { position.scrollTo(y: stack.offset(opening: k)) }
                }
                .offset(y: layout.headerY[k])
                .zIndex(1)
            }
        }
        .frame(maxWidth: .infinity, alignment: .topLeading)
    }

    private func header(_ rig: String, count: Int, open: Bool, tap: @escaping () -> Void) -> some View {
        Button(action: tap) {
            HStack(spacing: 9) {
                Text(open ? "▾" : "▸").foregroundStyle(open ? Theme.red : Theme.dim)
                Text(rig).fontWeight(.bold).foregroundStyle(Theme.bright)
                Spacer()
                Text("\(count)").foregroundStyle(Theme.soft)
            }
            .font(Theme.mono(.subheadline))
            .padding(.horizontal, 20)
            .frame(height: headerHeight)
            .background(Theme.bar)
            .overlay(alignment: .bottom) { Rectangle().fill(Theme.line).frame(height: 1) }
        }
        .buttonStyle(.plain)
        .accessibilityLabel("\(rig), \(count) panes")
        .accessibilityAddTraits(.isHeader)
    }

    private func rows(_ rig: HomeModel.RigPanes) -> some View {
        VStack(spacing: 0) {
            if rig.panes.isEmpty {
                Text("This rig has no panes.").font(Theme.mono(.footnote)).foregroundStyle(Theme.dim)
                    .frame(maxWidth: .infinity, alignment: .leading).padding(.horizontal, 20)
                    .frame(height: rowHeight)
            }
            ForEach(rig.panes, id: \.row.ref) { pane in
                SwipeRow(id: pane.row.ref, open: $openRow, label: pane.pinned ? "unpin" : "pin",
                         spoken: "\(pane.pinned ? "Unpin" : "Pin") \(pane.row.ref.pane)") {
                    Task { if pane.pinned { await home.unpin(pane.row.ref) } else { await home.pin(pane.row.ref) } }
                } content: {
                    HStack(spacing: 10) {
                        PaneRowView(row: pane.row)
                        if pane.pinned {
                            Text("pinned").font(Theme.mono(.caption2, .semibold)).foregroundStyle(Theme.red)
                                .accessibilityHidden(true)
                        }
                    }
                    .padding(.horizontal, 20)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .frame(height: rowHeight)
                    .background(shown(pane.row.ref) ? Theme.bar : Theme.bg)
                    .overlay(alignment: .leading) {
                        if shown(pane.row.ref) { Rectangle().fill(Theme.red).frame(width: 3) }
                    }
                    .contentShape(Rectangle())
                    .onTapGesture { tapped(pane.row.ref) }
                    .accessibilityValue(pane.pinned ? "pinned" : "")
                    .accessibilityAddTraits(.isButton)
                }
                .overlay(alignment: .bottom) { Rectangle().fill(Theme.line).frame(height: 1).padding(.leading, 20) }
            }
        }
        .background(Theme.bg)
    }
}

extension RigsAccordion {
    /// shown is true for the row whose pane is open beside the list.
    func shown(_ ref: PaneRef) -> Bool { besideThePane && home.selection == ref }

    /// tapped closes a row left slid open; otherwise it opens that row's
    /// conversation, pinned or not.
    func tapped(_ ref: PaneRef) {
        if openRow != nil {
            withAnimation(.snappy) { openRow = nil }
        } else {
            home.open(ref)
        }
    }
}

/// SwipeRow is a row with one action behind it: sliding it left shows the
/// action's key, and sliding it far does the action. VoiceOver gets the same
/// action by name. Only one row is open at a time, and scrolling closes it:
/// `open` is the row that is, shared by the screen.
struct SwipeRow<ID: Hashable, Content: View>: View {
    let id: ID
    @Binding var open: ID?
    let label: String
    let spoken: String
    let action: () -> Void
    @ViewBuilder var content: Content
    /// live is how far the finger has moved it during a slide.
    @State private var live: CGFloat = 0
    @State private var sliding = false
    private let key: CGFloat = 96

    private var rest: CGFloat { open == id ? -key : 0 }

    var body: some View {
        let x = max(-key * 1.8, min(0, rest + (sliding ? live : 0)))
        ZStack(alignment: .trailing) {
            if x < -1 {
                Button {
                    action()
                    withAnimation(.snappy) { open = nil }
                } label: {
                    Text(label).font(Theme.mono(.footnote, .bold)).foregroundStyle(Theme.bg)
                        .frame(width: max(key, -x)).frame(maxHeight: .infinity)
                        .background(Theme.red)
                }
                .buttonStyle(.plain)
                .accessibilityLabel(spoken)
            }
            content
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Theme.bg)
                .offset(x: x)
        }
        .clipped()
        .contentShape(Rectangle())
        .gesture(SidewaysPan { moved in
            sliding = true
            live = moved
        } ended: { moved in
            let end = rest + moved
            sliding = false
            live = 0
            if end < -key * 1.6 { action() }
            withAnimation(.snappy) { open = end < -key / 2 && end >= -key * 1.6 ? id : nil }
        })
        .accessibilityAction(named: Text(spoken)) { action() }
    }
}

/// SidewaysPan is a slide that only ever starts sideways. A drag that sets
/// off more up or down than across is refused at once, and the scroll view's
/// own drag waits for that refusal, so scrolling never has to compete with a
/// row: a gesture with no sense of direction woke for vertical drags too, and
/// the list would not scroll until the finger was wiggled.
struct SidewaysPan: UIGestureRecognizerRepresentable {
    var changed: (CGFloat) -> Void
    var ended: (CGFloat) -> Void

    func makeCoordinator(converter: CoordinateSpaceConverter) -> Coordinator { Coordinator() }

    func makeUIGestureRecognizer(context: Context) -> UIPanGestureRecognizer {
        let pan = UIPanGestureRecognizer()
        pan.delegate = context.coordinator
        return pan
    }

    func handleUIGestureRecognizerAction(_ pan: UIPanGestureRecognizer, context: Context) {
        let moved = pan.translation(in: pan.view).x
        switch pan.state {
        case .changed: changed(moved)
        case .ended: ended(moved)
        case .cancelled, .failed: ended(0)
        default: break
        }
    }

    final class Coordinator: NSObject, UIGestureRecognizerDelegate {
        /// Sideways only: across must clearly beat up-and-down at the first movement.
        func gestureRecognizerShouldBegin(_ recognizer: UIGestureRecognizer) -> Bool {
            guard let pan = recognizer as? UIPanGestureRecognizer else { return false }
            let v = pan.velocity(in: pan.view)
            return abs(v.x) > abs(v.y) * 1.3
        }

        /// A scroll view's drag waits for this one to refuse or take the touch.
        func gestureRecognizer(_ recognizer: UIGestureRecognizer, shouldBeRequiredToFailBy other: UIGestureRecognizer) -> Bool {
            other.view is UIScrollView
        }
    }
}
