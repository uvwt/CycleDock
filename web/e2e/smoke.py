"""Headless browser smoke tests against e2e/mock-server.mjs.

Run: MOCK_PORT=4190 node e2e/mock-server.mjs
     python e2e/smoke.py http://127.0.0.1:4190
This fixture is not a substitute for PostgreSQL integration testing.
"""
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

    def test_chat_creation_and_unified_timeline(self):
        self.page.goto(BASE + "/chat")
        self.page.get_by_role("heading", name="聊天").wait_for()
        self.page.get_by_role("link", name="讨论服务器巡检计划").first.click()
        self.assertIn("/chat/demo-project/discussion-1", self.page.url)
        self.page.get_by_text("我们需要每天自动检查服务器状态。").wait_for()
        self.page.get_by_text("建议监控 CPU、内存和容器健康").wait_for()
        self.page.get_by_role("button", name="生成任务").wait_for()
        self.page.get_by_role("button", name="返回").click()
        self.page.get_by_role("button", name="新建讨论").click()
        self.page.wait_for_url("**/chat/demo-project/discussion-*")
        self.page.get_by_role("textbox", name="发送消息，继续推进任务…").fill("把服务器巡检交给 AI 负责")
        self.page.get_by_role("button", name="发送", exact=True).click()
        self.page.get_by_text("把服务器巡检交给 AI 负责").wait_for()

    def test_board_and_card_drawer_and_stream(self):
        self.page.goto(BASE + "/board/demo-project")
        self.page.get_by_text("每天检查服务器健康").wait_for()
        self.page.get_by_role("button", name="检查 RN 服务器异常日志").first.click()
        self.page.get_by_role("button", name="任务详情").wait_for()
        self.page.get_by_text("状态变化 · todo -> working").wait_for()
        self.page.get_by_text("已定位 Docker 容器的错误记录。").wait_for(timeout=8000)
        self.page.get_by_role("button", name="任务详情").click()
        self.page.get_by_role("dialog", name="任务详情").wait_for()
        self.page.get_by_role("heading", name="当前执行").wait_for()
        self.page.get_by_role("button", name="关闭").click()
        self.assertEqual(self.page.get_by_role("dialog").count(), 0)

    def test_settings_and_responsive_layout(self):
        self.page.goto(BASE + "/settings/models")
        self.page.get_by_role("heading", name="模型服务", exact=True).first.wait_for()
        self.page.get_by_text("OpenAI-compatible API").wait_for()
        self.page.get_by_role("link", name="Agent 设置").click()
        self.page.get_by_text("Workspace Agent Instructions").wait_for()
        self.page.get_by_role("link", name="MCP 服务").click()
        self.page.get_by_role("button", name="添加连接").click()
        self.page.get_by_role("dialog", name="添加 MCP 服务").wait_for()
        self.page.get_by_role("textbox", name="连接名称").fill("测试 MCP")
        self.page.get_by_role("textbox", name="Remote MCP URL").fill("https://example.com/mcp")
        self.page.get_by_role("button", name="测试", exact=True).click()
        self.page.get_by_text("测试连接成功", exact=False).wait_for()
        self.page.get_by_role("button", name="关闭").click()
        self.page.set_viewport_size({"width": 375, "height": 812})
        self.page.goto(BASE + "/chat")
        self.page.get_by_role("heading", name="聊天").wait_for()
        self.assertLessEqual(self.page.evaluate("document.documentElement.scrollWidth"), 390)


    def test_persistent_language_and_theme(self):
        self.page.goto(BASE + "/settings/general")
        self.page.get_by_role("combobox", name="界面语言").select_option("en")
        self.page.get_by_role("heading", name="Settings", exact=True).wait_for()
        self.page.get_by_role("link", name="Model providers").wait_for()
        self.page.get_by_role("combobox", name="Appearance").select_option("dark")
        self.assertEqual(self.page.evaluate("document.documentElement.dataset.theme"), "dark")
        surface = self.page.locator(".bg-slate-50").first.evaluate(
            "(element) => getComputedStyle(element).backgroundColor"
        )
        self.assertNotEqual(surface, "rgb(248, 250, 252)")
        self.page.reload()
        self.page.get_by_role("heading", name="Settings", exact=True).wait_for()
        self.assertEqual(self.page.get_by_role("combobox", name="Appearance").input_value(), "dark")
        self.assertEqual(self.page.get_by_role("combobox", name="Language").input_value(), "en")
        self.assertEqual(self.page.evaluate("document.documentElement.lang"), "en")
        self.page.get_by_role("link", name="Chat", exact=True).click()
        self.page.get_by_role("heading", name="Chat", exact=True).wait_for()
        self.page.get_by_role("button", name="New discussion").wait_for()
        self.assertEqual(self.page.evaluate("document.documentElement.dataset.theme"), "dark")


if __name__ == "__main__":
    unittest.main(verbosity=2)
