"""Frontend V2 SSE 断线/快照/旧 Run 事件浏览器回归。

运行：python e2e/sse_reconnect.py http://127.0.0.1:4188
依赖 mock-server.mjs 提供构建产物与隔离 API fixture；不替代真实服务端 SSE/网络验证。
"""
import sys
import unittest
from playwright.sync_api import sync_playwright

BASE = sys.argv.pop(1) if len(sys.argv) > 1 else "http://127.0.0.1:4188"

# 保留真实页面、React、Query 与 REST；仅控制浏览器 EventSource 时序。
FAKE_STREAM = """
window.__testSSE = null;
class ControlledSSE extends EventTarget {
  constructor(url) {
    super();
    this.url = url;
    window.__testSSE = this;
  }
  emit(name, payload) {
    this.dispatchEvent(new MessageEvent(name, { data: JSON.stringify(payload) }));
  }
  open() { this.onopen?.(new Event('open')); }
  fail() { this.onerror?.(new Event('error')); }
  close() {}
}
window.EventSource = ControlledSSE;
"""


class ReconnectTests(unittest.TestCase):
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
        self.page.on("pageerror", lambda e: self.errors.append(str(e)))
        self.page.add_init_script(FAKE_STREAM)
        self.page.goto(BASE + "/projects/demo-project/work/todo-1")
        self.page.get_by_role("heading", name="检查 RN 服务器异常日志").wait_for()
        self.page.wait_for_function("window.__testSSE !== null")
        self.page.evaluate("window.__testSSE.open()")

    def tearDown(self):
        self.assertEqual(self.errors, [])
        self.page.close()

    def emit(self, name, payload):
        self.page.evaluate("([n, p]) => window.__testSSE.emit(n, p)", [name, payload])

    def test_disconnect_and_open_preserve_live_text_until_reconciled(self):
        self.emit("run.started", {"run_id": "run-demo"})
        self.emit("run.text.delta", {"run_id": "run-demo", "text": "重连前的未持久化文本"})
        self.page.get_by_text("重连前的未持久化文本").wait_for()
        self.page.evaluate("window.__testSSE.fail()")
        self.page.evaluate("window.__testSSE.open()")
        self.page.wait_for_timeout(600)
        self.assertEqual(self.page.get_by_text("重连前的未持久化文本").count(), 1)
        self.emit("run.text.snapshot", {
            "run_id": "run-demo", "text": "服务端完整快照"
        })
        self.page.get_by_text("服务端完整快照").wait_for()
        self.assertEqual(self.page.get_by_text("重连前的未持久化文本").count(), 0)
        self.emit("run.text.delta", {"run_id": "run-demo", "text": " + 新增"})
        self.page.get_by_text("服务端完整快照 + 新增").wait_for()

    def test_stale_run_delta_and_final_cannot_overwrite_new_run(self):
        self.emit("run.started", {"run_id": "run-demo"})
        self.emit("run.text.delta", {"run_id": "run-demo", "text": "旧任务输出"})
        self.page.get_by_text("旧任务输出").wait_for()
        self.emit("run.started", {"run_id": "run-next"})
        self.emit("run.text.delta", {"run_id": "run-next", "text": "新任务输出"})
        self.page.get_by_text("新任务输出").wait_for()
        self.emit("run.text.delta", {"run_id": "run-demo", "text": "旧任务迟到"})
        self.emit("run.failed", {"run_id": "run-demo"})
        self.page.wait_for_timeout(600)
        self.assertEqual(self.page.get_by_text("新任务输出").count(), 1)
        self.assertEqual(self.page.get_by_text("旧任务迟到").count(), 0)
        self.emit("run.finished", {"run_id": "run-next"})
        self.page.get_by_text("新任务输出").wait_for(state="detached")


if __name__ == "__main__":
    unittest.main(verbosity=2)
