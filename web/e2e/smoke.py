"""Headless browser smoke tests against e2e/mock-server.mjs.

Run: MOCK_PORT=4190 node e2e/mock-server.mjs
     python e2e/smoke.py http://127.0.0.1:4190
This fixture is not a substitute for PostgreSQL integration testing.
"""
import re
import sys
import unittest

from playwright.sync_api import sync_playwright

BASE = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:4188"
if len(sys.argv) > 1:
    sys.argv.pop(1)


class CycleDockSmoke(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.engine = sync_playwright().start()
        cls.browser = cls.engine.chromium.launch(headless=True)

    @classmethod
    def tearDownClass(cls):
        cls.browser.close()
        cls.engine.stop()

    def setUp(self):
        self.page = self.browser.new_page(viewport={"width": 1280, "height": 860})
        self.errors = []
        self.page.on("pageerror", lambda error: self.errors.append(str(error)))

    def tearDown(self):
        self.assertEqual(self.errors, [], "Browser JavaScript errors")
        self.page.close()

    def test_discussion_workbench_composer_and_time_separator(self):
        self.page.goto(BASE + "/chat")
        self.page.get_by_role("heading", name="今天想推进什么？").wait_for()
        self.page.locator('aside[aria-label="讨论列表"]').wait_for()
        self.page.get_by_role(
            "button", name=re.compile("讨论服务器巡检计划")
        ).first.click()

        self.page.wait_for_url("**/chat/demo-project/discussion-1")
        self.page.get_by_text("我们需要每天自动检查服务器状态。").wait_for()
        self.page.get_by_text("建议监控 CPU、内存和容器健康").wait_for()

        self.assertEqual(self.page.locator("[data-time-separator]").count(), 1)
        self.page.locator('aside[aria-label="任务详情"]').wait_for()
        self.page.get_by_role("button", name="生成任务").wait_for()

        collapsed = self.page.locator(
            'input[aria-label="发送消息，继续推进任务…"]'
        )
        collapsed.wait_for()
        collapsed.focus()
        self.page.locator(
            'textarea[aria-label="发送消息，继续推进任务…"]'
        ).wait_for()

    def test_welcome_message_creates_discussion_and_sends_first_turn(self):
        self.page.goto(BASE + "/chat")
        collapsed = self.page.locator(
            'input[aria-label="发送消息，开始讨论…"]'
        )
        collapsed.wait_for()
        collapsed.focus()
        expanded = self.page.locator(
            'textarea[aria-label="发送消息，开始讨论…"]'
        )
        expanded.fill("把服务器巡检交给 AI 负责")
        self.page.get_by_role("button", name="发送", exact=True).click()

        self.page.wait_for_url("**/chat/demo-project/discussion-*")
        self.page.get_by_text("把服务器巡检交给 AI 负责").wait_for()

    def test_board_manual_todo_and_default_details(self):
        self.page.goto(BASE + "/board/demo-project")
        self.page.get_by_role("button", name="新建待办").click()
        dialog = self.page.get_by_role("dialog", name="新建待办")
        dialog.wait_for()
        dialog.get_by_role("textbox", name="待办标题").fill("手工填写待办")
        dialog.get_by_role("textbox", name="待办描述").fill(
            "这个待办不经过模型生成"
        )
        dialog.get_by_role("button", name="创建待办").click()

        self.page.wait_for_url("**/cards/demo-project/todo-*")
        self.page.get_by_role("heading", name="手工填写待办").first.wait_for()
        details = self.page.locator('aside[aria-label="任务详情"]')
        details.wait_for()
        details.get_by_text("这个待办不经过模型生成").wait_for()

    def test_existing_todo_stream_and_desktop_details(self):
        self.page.goto(BASE + "/board/demo-project")
        self.page.get_by_role(
            "button", name=re.compile("检查 RN 服务器异常日志")
        ).first.click()

        self.page.wait_for_url("**/cards/demo-project/todo-1")
        details = self.page.locator('aside[aria-label="任务详情"]')
        details.wait_for()
        details.get_by_role("heading", name="当前执行").wait_for()
        self.page.get_by_text("状态变化 · todo -> working").wait_for()
        self.page.get_by_text(
            "已定位 Docker 容器的错误记录。"
        ).wait_for(timeout=8000)

    def test_mobile_discussion_and_details_use_drawers(self):
        self.page.set_viewport_size({"width": 375, "height": 812})
        self.page.goto(BASE + "/chat")
        self.page.get_by_role("heading", name="今天想推进什么？").wait_for()
        self.assertEqual(
            self.page.locator('nav[aria-label="主导航"]:visible').count(), 1
        )
        self.page.get_by_role("button", name="打开讨论列表").click()
        discussion_dialog = self.page.get_by_role("dialog", name="讨论列表")
        discussion_dialog.wait_for()
        discussion_dialog.get_by_role(
            "button", name=re.compile("讨论服务器巡检计划")
        ).click()

        self.page.wait_for_url("**/chat/demo-project/discussion-1")
        self.page.locator(
            'header nav[aria-label="主导航"]'
        ).wait_for(state="detached")
        self.assertEqual(
            self.page.locator('nav[aria-label="主导航"]:visible').count(), 0
        )
        self.page.get_by_role("button", name="打开讨论列表").wait_for()
        self.page.get_by_role("button", name="任务详情").click()
        self.page.get_by_role("dialog", name="任务详情").wait_for()
        self.assertLessEqual(
            self.page.evaluate("document.documentElement.scrollWidth"), 390
        )

    def test_settings_and_persistent_language_theme(self):
        self.page.goto(BASE + "/settings/general")
        self.page.get_by_role("combobox", name="界面语言").select_option("en")
        self.page.get_by_role("heading", name="Settings", exact=True).wait_for()
        self.page.get_by_role("combobox", name="Appearance").select_option(
            "dark"
        )
        self.assertEqual(
            self.page.evaluate("document.documentElement.dataset.theme"),
            "dark",
        )
        surface = self.page.locator(".bg-slate-50").first.evaluate(
            "(element) => getComputedStyle(element).backgroundColor"
        )
        self.assertNotEqual(surface, "rgb(248, 250, 252)")
        self.page.reload()
        self.page.get_by_role("heading", name="Settings", exact=True).wait_for()
        self.assertEqual(
            self.page.get_by_role(
                "combobox", name="Appearance"
            ).input_value(),
            "dark",
        )
        self.assertEqual(
            self.page.get_by_role("combobox", name="Language").input_value(),
            "en",
        )
        self.assertEqual(
            self.page.evaluate("document.documentElement.lang"), "en"
        )
        self.page.get_by_role("link", name="Chat", exact=True).click()
        self.page.get_by_role(
            "heading", name="What would you like to move forward today?"
        ).wait_for()
        self.page.get_by_role(
            "button", name="New discussion", exact=True
        ).wait_for()
        self.assertEqual(
            self.page.evaluate("document.documentElement.dataset.theme"),
            "dark",
        )


if __name__ == "__main__":
    unittest.main(verbosity=2)
