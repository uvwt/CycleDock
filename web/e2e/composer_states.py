"""Frontend V3 adaptive composer behavior against the isolated fixture.

python e2e/composer_states.py http://127.0.0.1:4188
"""
import sys
import unittest
from playwright.sync_api import sync_playwright

BASE = sys.argv.pop(1) if len(sys.argv) > 1 else "http://127.0.0.1:4188"


class ComposerStateTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.pw = sync_playwright().start()
        cls.browser = cls.pw.chromium.launch(headless=True)

    @classmethod
    def tearDownClass(cls):
        cls.browser.close()
        cls.pw.stop()

    def setUp(self):
        self.page = self.browser.new_page(viewport={"width": 1440, "height": 900})
        self.errors = []
        self.page.on("pageerror", lambda error: self.errors.append(str(error)))

    def tearDown(self):
        self.assertEqual(self.errors, [])
        self.page.close()

    def test_discussion_composer_expands_collapses_and_keeps_nonempty_draft(self):
        p = self.page
        p.goto(BASE + "/projects/demo-project/discussions")
        form = p.locator("form.product-composer")
        editor = p.get_by_role("textbox", name="新讨论内容")
        form.wait_for()

        self.assertEqual(form.get_attribute("data-expanded"), "false")
        self.assertEqual(editor.get_attribute("rows"), "1")

        editor.focus()
        p.wait_for_function(
            "document.querySelector('form.product-composer')?.dataset.expanded === 'true'"
        )
        self.assertEqual(editor.get_attribute("rows"), "3")

        editor.fill("保留这段产品化草稿")
        editor.evaluate("(el) => el.blur()")
        self.assertEqual(form.get_attribute("data-expanded"), "true")
        self.assertEqual(editor.input_value(), "保留这段产品化草稿")

        editor.fill("")
        editor.evaluate("(el) => el.blur()")
        p.wait_for_function(
            "document.querySelector('form.product-composer')?.dataset.expanded === 'false'"
        )
        self.assertEqual(editor.get_attribute("rows"), "1")

    def test_card_composer_running_state_remains_expanded_after_blur(self):
        p = self.page
        p.goto(BASE + "/projects/demo-project/work/todo-1")
        form = p.locator("form.product-composer")
        editor = p.get_by_role("textbox", name="回复内容")
        form.wait_for()

        self.assertEqual(form.get_attribute("data-expanded"), "false")
        timeline = p.locator("[data-timeline]")
        timeline.evaluate("(el) => { el.scrollTop = el.scrollHeight }")
        editor.focus()
        p.wait_for_function(
            "document.querySelector('form.product-composer')?.dataset.expanded === 'true'"
        )
        p.wait_for_timeout(80)
        bottom_gap = timeline.evaluate(
            "(el) => el.scrollHeight - el.scrollTop - el.clientHeight"
        )
        # flex 布局和子像素取整会留下极小余量；12px 内视为视觉贴底。
        self.assertLessEqual(bottom_gap, 12)
        editor.evaluate("(el) => el.blur()")
        self.assertEqual(form.get_attribute("data-expanded"), "true")
        p.get_by_role("combobox", name="新指令处理方式").wait_for()

    def test_successful_send_returns_card_composer_to_compact_state(self):
        p = self.page
        p.goto(BASE + "/projects/demo-project/work/todo-1")
        form = p.locator("form.product-composer")
        editor = p.get_by_role("textbox", name="回复内容")
        editor.fill("产品化 Composer 回归")
        self.assertEqual(form.get_attribute("data-expanded"), "true")
        p.get_by_role("button", name="发送", exact=True).click()
        p.wait_for_function(
            "document.querySelector('textarea[aria-label=\"回复内容\"]')?.value === ''"
        )
        p.wait_for_function(
            "document.querySelector('form.product-composer')?.dataset.expanded === 'false'"
        )
        self.assertEqual(form.get_attribute("data-expanded"), "false")

    def test_mobile_focused_composer_has_no_root_overflow(self):
        p = self.page
        p.set_viewport_size({"width": 390, "height": 844})
        p.goto(BASE + "/projects/demo-project/discussions")
        form = p.locator("form.product-composer")
        editor = p.get_by_role("textbox", name="新讨论内容")
        editor.focus()
        p.wait_for_function(
            "document.querySelector('form.product-composer')?.dataset.expanded === 'true'"
        )
        box = form.bounding_box()
        self.assertIsNotNone(box)
        self.assertLessEqual(box["x"] + box["width"], 390.5)
        metrics = p.evaluate(
            "() => ({width: innerWidth, scrollWidth: document.documentElement.scrollWidth})"
        )
        self.assertEqual(metrics["width"], 390)
        self.assertEqual(metrics["scrollWidth"], 390)
        p.get_by_role("button", name="发送", exact=True).wait_for()


if __name__ == "__main__":
    unittest.main(verbosity=2)
