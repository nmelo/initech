import InitechCore
import SwiftUI
import UIKit

/// BackSwipe turns the system's swipe from the left edge back on for a
/// stacked screen that hides the navigation bar. With the bar hidden the
/// system refuses that swipe; this answers for it while the screen is up,
/// allowing it whenever there is a screen to go back to, and hands the answer
/// back to the system when the screen leaves. The swipe stays the system's
/// own: the screen follows the finger and can be let go half way.
struct BackSwipe: UIViewControllerRepresentable {
    func makeUIViewController(context: Context) -> Holder { Holder() }
    func updateUIViewController(_ holder: Holder, context: Context) {}

    final class Holder: UIViewController, UIGestureRecognizerDelegate {
        /// taken is each navigation stack above this screen with the answer it had before.
        private var taken: [(nav: UINavigationController, was: UIGestureRecognizerDelegate?)] = []

        override func viewDidAppear(_ animated: Bool) {
            super.viewDidAppear(animated)
            guard taken.isEmpty else { return }
            var node = parent
            while let controller = node {
                if let nav = controller as? UINavigationController, let pop = nav.interactivePopGestureRecognizer {
                    taken.append((nav, pop.delegate))
                    pop.delegate = self
                    pop.isEnabled = true
                }
                node = controller.parent
            }
            let depths = taken.map { String($0.nav.viewControllers.count) }.joined(separator: ",")
            Log.screen.notice("back swipe: \(self.taken.count, privacy: .public) stacks, depths \(depths, privacy: .public)")
        }

        override func viewDidDisappear(_ animated: Bool) {
            super.viewDidDisappear(animated)
            for (nav, was) in taken where nav.interactivePopGestureRecognizer?.delegate === self {
                nav.interactivePopGestureRecognizer?.delegate = was
            }
            taken = []
        }

        /// The swipe begins only on a stack that has a screen to go back to and is not already moving.
        func gestureRecognizerShouldBegin(_ recognizer: UIGestureRecognizer) -> Bool {
            guard let nav = taken.first(where: { $0.nav.interactivePopGestureRecognizer === recognizer })?.nav else { return false }
            return nav.viewControllers.count > 1 && nav.transitionCoordinator == nil
        }
    }
}
