"""Frontend V2 主业务路径：运行 production build + 原有 mock-server。
python e2e/smoke.py http://127.0.0.1:4188
这些浏览器测试不代替真实后端并发、OAuth 和跨实例 SSE 验证。
"""
import sys
import unittest
from playwright.sync_api import sync_playwright

BASE = sys.argv[1] if len(sys.argv)>1 else "http://127.0.0.1:4188"
if len(sys.argv)>1:
    sys.argv.pop(1)

class FrontendV2Smoke(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.playwright=sync_playwright().start()
        cls.browser=cls.playwright.chromium.launch(headless=True)

    @classmethod
    def tearDownClass(cls):
        cls.browser.close()
        cls.playwright.stop()

    def setUp(self):
        self.page=self.browser.new_page(viewport={"width":1440,"height":900})
        self.errors=[]
        self.page.on("pageerror",lambda error:self.errors.append(str(error)))

    def tearDown(self):
        self.assertEqual(self.errors,[],"Browser JS errors")
        self.page.close()

    def test_01_discussion_and_work_have_one_sidebar_and_scoped_mode(self):
        p=self.page
        p.goto(BASE+"/projects/demo-project/discussions")
        p.get_by_role("heading",name="今天想推进什么？").wait_for()
        self.assertEqual(p.locator('aside[aria-label="主导航"]').count(),1)
        p.locator('aside nav[aria-label="工作模式"]').get_by_role("button",name="工作").click()
        p.wait_for_url("**/projects/demo-project/work")
        p.get_by_role("heading",name="待办看板").wait_for()
        p.locator('aside button').filter(has_text="NexusDock 运维").last.click()
        p.get_by_role("menuitem",name="产品发布").click()
        p.wait_for_url("**/projects/demo-project-b/work")
        p.get_by_role("heading",name="工作").wait_for()
        p.locator('aside nav[aria-label="工作模式"]').get_by_role("button",name="讨论").click()
        p.wait_for_url("**/projects/demo-project-b/discussions")
        p.locator('aside button').filter(has_text="产品发布").last.click()
        p.get_by_role("menuitem",name="NexusDock 运维").click()
        p.wait_for_url("**/projects/demo-project/discussions")

    def test_01b_project_creation_preserves_both_modes(self):
        p=self.page
        for mode,label in [("discussions","V2 讨论项目"),("work","V2 工作项目")]:
            p.goto(BASE+"/projects/demo-project/"+mode)
            p.locator('aside').get_by_role("button",name="NexusDock 运维",exact=True).click()
            p.get_by_role("menuitem",name="新建项目").click()
            dialog=p.get_by_role("dialog")
            dialog.get_by_role("heading",name="新建项目").wait_for()
            dialog.get_by_role("textbox",name="项目名称").fill(label)
            dialog.get_by_role("button",name="创建",exact=True).click()
            p.wait_for_url("**/projects/project-*/"+mode)
            p.locator("aside").get_by_role("button",name=label,exact=True).wait_for()
            self.assertEqual(p.locator('aside nav[aria-label="工作模式"] button[aria-current="page"]').count(),1)
            expected="讨论" if mode=="discussions" else "工作"
            self.assertIn(expected,p.locator('aside nav[aria-label="工作模式"] button[aria-current="page"]').inner_text())
            p.reload()
            p.locator("aside").get_by_role("button",name=label,exact=True).wait_for()

    def test_02_board_create_todo_and_open_unified_thread(self):
        p=self.page
        p.goto(BASE+"/projects/demo-project/work")
        p.get_by_role("button",name="新建待办").click()
        p.get_by_role("dialog").get_by_role("heading",name="新建待办").wait_for()
        p.get_by_role("dialog").get_by_role("textbox",name="标题").fill("V2 手工待办测试")
        p.get_by_role("dialog").get_by_role("textbox",name="任务说明").fill("用户独立创建 Todo")
        p.get_by_role("dialog").get_by_role("button",name="创建待办").click()
        p.wait_for_url("**/projects/demo-project/work/todo-*")
        p.get_by_role("heading",name="V2 手工待办测试").wait_for()
        p.get_by_role("button",name="卡片信息").click()
        p.get_by_text("用户独立创建 Todo").wait_for()
        p.keyboard.press("Escape")
        p.get_by_role("combobox",name="卡片状态").click()
        p.get_by_role("option",name="进行中").click()
        p.get_by_role("button",name="卡片信息").click()
        p.get_by_text("编辑信息").wait_for()

    def test_03_source_events_and_agent_duration(self):
        p=self.page
        p.goto(BASE+"/projects/demo-project/work/todo-1")
        p.locator('[data-message-role="human"]').first.wait_for()
        self.assertGreaterEqual(p.locator('[data-message-role="agent"]').count(),1)
        self.assertEqual(p.locator('[data-message-role="human"] [data-run-duration]').count(),0)
        p.get_by_role("button",name="卡片信息").click()
        p.get_by_text("来源讨论").wait_for()
        p.keyboard.press("Escape")
        p.locator("textarea[aria-label='回复内容']").fill("按计划继续")
        p.get_by_role("button",name="发送",exact=True).click()
        p.locator("textarea[aria-label='回复内容']").wait_for()
        self.assertEqual(p.locator("textarea[aria-label='回复内容']").input_value(),"")
        p.wait_for_timeout(1700)
        self.assertEqual(self.errors,[])

    def test_04_first_message_failed_retry_preserves_card(self):
        p=self.page
        p.goto(BASE+"/projects/demo-project/discussions")
        p.get_by_role("textbox",name="新讨论内容").fill("V2 第一条消息失败保护")
        p.get_by_role("button",name="发送",exact=True).click()
        p.get_by_text("发送失败，内容与目标 Discussion 已保留。").wait_for()
        self.assertEqual(p.get_by_role("textbox",name="新讨论内容").input_value(),"V2 第一条消息失败保护")
        self.assertIn("首次发送尚未完成",p.locator("body").inner_text())
        p.reload()
        p.get_by_role("textbox",name="新讨论内容").wait_for()
        self.assertEqual(p.get_by_role("textbox",name="新讨论内容").input_value(),"V2 第一条消息失败保护")
        p.get_by_role("button",name="发送",exact=True).click()
        p.wait_for_url("**/projects/demo-project/discussions/discussion-*")

    def test_04a_discussion_creation_failure_keeps_unsubmitted_draft(self):
        p=self.page
        p.request.post(BASE+"/api/v1/__fixture/fail-next-discussion")
        p.goto(BASE+"/projects/demo-project/discussions")
        text="新建讨论失败后不能丢失原始内容"
        p.get_by_role("textbox",name="新讨论内容").fill(text)
        p.get_by_role("button",name="发送",exact=True).click()
        p.get_by_text("发送失败，内容与目标 Discussion 已保留。").wait_for()
        self.assertEqual(p.get_by_role("textbox",name="新讨论内容").input_value(),text)
        self.assertNotIn("首次发送尚未完成",p.locator("body").inner_text())
        p.reload()
        p.get_by_role("textbox",name="新讨论内容").wait_for()
        self.assertEqual(p.get_by_role("textbox",name="新讨论内容").input_value(),text)

    def test_04b_discussion_drafts_are_isolated_by_project(self):
        p=self.page
        p.goto(BASE+"/projects/demo-project/discussions")
        p.get_by_role("textbox",name="新讨论内容").fill("项目 A 独立草稿")
        p.locator("aside").get_by_role("button",name="NexusDock 运维",exact=True).click()
        p.get_by_role("menuitem",name="产品发布").click()
        p.wait_for_url("**/projects/demo-project-b/discussions")
        p.get_by_role("textbox",name="新讨论内容").wait_for()
        p.wait_for_function("document.querySelector('textarea[aria-label=\\\"新讨论内容\\\"]')?.value === ''")
        p.get_by_role("textbox",name="新讨论内容").fill("项目 B 独立草稿")
        p.locator("aside").get_by_role("button",name="产品发布",exact=True).click()
        p.get_by_role("menuitem",name="NexusDock 运维").click()
        p.wait_for_url("**/projects/demo-project/discussions")
        p.wait_for_function("document.querySelector('textarea[aria-label=\"新讨论内容\"]')?.value === '项目 A 独立草稿'")
        p.reload()
        self.assertEqual(p.get_by_role("textbox",name="新讨论内容").input_value(),"项目 A 独立草稿")

    def test_04c_ongoing_schedule_requires_explicit_editing(self):
        p=self.page
        p.goto(BASE+"/projects/demo-project/work/ongoing-1")
        p.get_by_role("button",name="卡片信息").click()
        p.get_by_text("每天 09:00 · Asia/Shanghai").wait_for()
        self.assertEqual(p.get_by_role("combobox",name="计划周期").count(),0)
        p.get_by_role("button",name="编辑",exact=True).click()
        p.get_by_role("combobox",name="计划周期").click()
        p.get_by_role("option",name="每周").click()
        p.get_by_role("combobox",name="星期").click()
        p.get_by_role("option",name="周三").click()
        p.get_by_role("button",name="保存日程").click()
        p.get_by_text("每周周三 09:00 · Asia/Shanghai").wait_for()
        p.get_by_role("button",name="新增日程").click()
        p.get_by_role("combobox",name="计划周期").click()
        p.get_by_role("option",name="每月").click()
        p.get_by_role("spinbutton",name="每月日期（1–28）").fill("29")
        self.assertTrue(p.get_by_role("spinbutton",name="每月日期（1–28）").evaluate("(el) => el.validity.rangeOverflow"))
        p.get_by_role("spinbutton",name="每月日期（1–28）").fill("15")
        p.get_by_role("button",name="新增日程").last.click()
        p.get_by_text("每月 15 日 09:00 · Asia/Shanghai").wait_for()

    def test_04d_direct_routes_refresh_and_browser_history(self):
        p=self.page
        p.goto(BASE+"/board/demo-project")
        p.wait_for_url("**/projects/demo-project/work")
        p.goto(BASE+"/chat/demo-project/discussion-1")
        p.wait_for_url("**/projects/demo-project/discussions/discussion-1")
        p.get_by_role("heading",name="讨论服务器巡检计划").wait_for()
        p.goto(BASE+"/cards/demo-project/todo-1")
        p.wait_for_url("**/projects/demo-project/work/todo-1")
        p.get_by_role("heading",name="检查 RN 服务器异常日志").wait_for()
        p.reload()
        p.get_by_role("heading",name="检查 RN 服务器异常日志").wait_for()
        p.get_by_role("button",name="卡片信息").click()
        p.get_by_role("button",name="打开原讨论").click()
        p.wait_for_url("**/projects/demo-project/discussions/discussion-1")
        p.go_back()
        p.wait_for_url("**/projects/demo-project/work/todo-1")
        p.get_by_role("heading",name="检查 RN 服务器异常日志").wait_for()

    def test_05_settings_sections_and_theme(self):
        p=self.page
        p.goto(BASE+"/settings/general")
        p.get_by_role("heading",name="设置",exact=True).wait_for()
        p.get_by_role("combobox",name="外观").click()
        p.get_by_role("option",name="深色").click()
        self.assertEqual(p.locator("html").get_attribute("data-theme"),"dark")
        p.get_by_role("combobox",name="外观").click()
        p.get_by_role("option",name="浅色").click()
        p.get_by_role("navigation",name="设置分类").get_by_role("button",name="项目设置").click()
        p.wait_for_url("**/settings/project")
        p.get_by_role("heading",name="项目设置",exact=True).wait_for()
        p.get_by_role("textbox",name="名称").wait_for()
        p.get_by_role("navigation",name="设置分类").get_by_role("button",name="MCP 服务").click()
        p.wait_for_url("**/settings/mcp")
        p.get_by_role("heading",name="MCP 服务",exact=True).wait_for()

    def test_05a_agent_and_mcp_forms_retain_test_endpoints(self):
        p=self.page
        p.goto(BASE+"/settings/agent")
        p.get_by_role("heading",name="Agent 设置",exact=True).wait_for()
        p.get_by_role("button",name="测试当前模型").click()
        p.get_by_text("测试连接成功").wait_for()
        p.get_by_role("button",name="保存 Agent 配置").click()
        p.get_by_text("Agent 设置已保存").wait_for()
        p.goto(BASE+"/settings/mcp")
        p.get_by_role("button",name="编辑",exact=True).click()
        p.get_by_role("button",name="测试连接").click()
        p.get_by_text("测试连接成功").wait_for()
        p.get_by_role("button",name="保存修改").click()
        p.get_by_text("MCP 连接已保存").wait_for()

    def test_05aa_authorized_models_update_form_value_and_persist(self):
        p=self.page
        p.request.post(BASE+"/api/v1/__fixture/connect-grok")
        p.goto(BASE+"/settings/agent")
        p.get_by_role("combobox",name="模型来源").click()
        p.get_by_role("option",name="Grok Subscription").click()
        p.get_by_role("combobox",name="已授权模型目录").click()
        p.get_by_role("option",name="Grok Mini (测试目录)").click()
        self.assertEqual(p.get_by_role("textbox",name="模型 ID").input_value(),"grok-3-mini")
        p.get_by_role("button",name="测试当前模型").click()
        p.get_by_text("测试连接成功").wait_for()
        p.get_by_role("button",name="保存 Agent 配置").click()
        p.get_by_text("Agent 设置已保存").wait_for()
        p.reload()
        self.assertEqual(p.get_by_role("textbox",name="模型 ID").input_value(),"grok-3-mini")

    def test_05b_sse_updates_live_agent_without_human_bubble(self):
        p=self.page
        p.goto(BASE+"/projects/demo-project/work/todo-1")
        p.locator('[data-message-role="agent"] [data-run-duration]').first.wait_for()
        p.get_by_text("定时巡检触发").wait_for()
        p.get_by_text("正在读取 RN 服务器日志。").wait_for(timeout=7000)
        self.assertEqual(p.locator('[data-message-role="human"] [data-run-duration]').count(),0)
        self.assertEqual(p.get_by_text("正在读取 RN 服务器日志。").count(),1)
        p.get_by_role("button",name="取消排队").click()
        p.get_by_role("button",name="重试",exact=True).first.click()

    def test_06_mobile_no_root_overflow_and_sheet(self):
        p=self.page
        p.set_viewport_size({"width":390,"height":844})
        for url in [
            "/projects/demo-project/discussions",
            "/projects/demo-project/work",
            "/projects/demo-project/work/todo-1",
            "/settings/general",
        ]:
            p.goto(BASE+url)
            p.locator(".v2").first.wait_for()
            p.wait_for_timeout(200)
            metrics=p.evaluate("() => ({document:document.documentElement.scrollWidth,body:document.body.scrollWidth,viewport:innerWidth})")
            self.assertLessEqual(metrics["document"],390,f"{url}: {metrics}")
            self.assertLessEqual(metrics["body"],390,f"{url}: {metrics}")
        p.goto(BASE+"/projects/demo-project/work/todo-1")
        p.get_by_role("button",name="打开导航").click()
        p.get_by_role("dialog").get_by_role("heading",name="工作导航").wait_for()
        p.keyboard.press("Escape")
        p.get_by_role("button",name="卡片信息").click()
        p.get_by_role("dialog").get_by_role("heading",name="卡片信息").wait_for()

    def test_07_workspace_switch_invalidates_old_scope(self):
        p=self.page
        p.goto(BASE+"/projects/demo-project/work")
        p.locator("aside").get_by_role("button",name="测试用户",exact=False).click()
        p.get_by_role("menuitem",name="第二空间").click()
        p.wait_for_url("**/projects/demo-project-b/discussions")
        p.locator("aside").get_by_role("button",name="产品发布",exact=True).wait_for()
        p.locator("aside").get_by_role("button",name="测试用户",exact=False).click()
        p.get_by_text("第二空间",exact=True).wait_for()
        p.keyboard.press("Escape")
        p.goto(BASE+"/settings/models")
        p.get_by_role("heading",name="需要所有者权限").wait_for()
        p.goto(BASE+"/projects/demo-project/work")
        p.get_by_role("heading",name="项目不可访问").wait_for()

if __name__=="__main__":
    unittest.main(verbosity=2)
