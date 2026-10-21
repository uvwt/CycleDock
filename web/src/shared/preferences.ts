import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import { useSyncExternalStore } from "react";

export type Language = "zh" | "en";
export type Theme = "light" | "dark" | "system";

const LANGUAGE_KEY = "cycledock.language";
const THEME_KEY = "cycledock.theme";

function getStoredLanguage(): Language {
  try {
    const value = localStorage.getItem(LANGUAGE_KEY);
    return value === "en" ? "en" : "zh";
  } catch {
    return "zh";
  }
}

function getStoredTheme(): Theme {
  try {
    const value = localStorage.getItem(THEME_KEY);
    return value === "dark" || value === "system" ? value : "light";
  } catch {
    return "light";
  }
}

let theme: Theme = getStoredTheme();
const listeners = new Set<() => void>();
let media: MediaQueryList | undefined;

// 主题是本设备的界面偏好，不属于 Workspace，也不进入服务端业务模型。
function applyTheme() {
  const dark = theme === "dark" || (theme === "system" && media?.matches);
  document.documentElement.dataset.theme = dark ? "dark" : "light";
  document.documentElement.style.colorScheme = dark ? "dark" : "light";
}

export function initializePreferences() {
  media = window.matchMedia("(prefers-color-scheme: dark)");
  media.addEventListener("change", applyTheme);
  window.addEventListener("storage", (event) => {
    if (event.key === THEME_KEY) {
      theme = getStoredTheme();
      applyTheme();
      listeners.forEach((listener) => listener());
    }
    if (event.key === LANGUAGE_KEY) {
      const language = getStoredLanguage();
      void i18n.changeLanguage(language);
      document.documentElement.lang = language === "en" ? "en" : "zh-CN";
    }
  });
  applyTheme();
}

export function setTheme(next: Theme) {
  theme = next;
  try {
    localStorage.setItem(THEME_KEY, next);
  } catch {
    /* browsing without storage */
  }
  applyTheme();
  listeners.forEach((listener) => listener());
}
export function useTheme(): [Theme, (next: Theme) => void] {
  const current = useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    () => theme,
  );
  return [current, setTheme];
}

const en = {
  聊天: "Chat",
  看板: "Board",
  讨论: "Discussion",
  工作: "Work",
  工作视图: "Work views",
  全部工作: "All work",
  待我审核: "Needs my review",
  打开导航: "Open navigation",
  设置: "Settings",
  导航抽屉: "Navigation menu",
  退出登录: "Sign out",
  工作空间: "Workspace",
  常规: "General",
  账户: "Account",
  模型服务: "Model providers",
  "Agent 设置": "Agent settings",
  "MCP 服务": "MCP services",
  项目设置: "Project settings",
  个人: "Personal",
  "AI 配置": "AI configuration",
  外部连接: "External connections",
  显示与偏好: "Display & preferences",
  界面语言: "Language",
  外观: "Appearance",
  简体中文: "Simplified Chinese",
  英语: "English",
  浅色: "Light",
  深色: "Dark",
  跟随系统: "System",
  "选择界面语言与颜色模式，偏好仅保存在当前浏览器。":
    "Choose your language and appearance. Preferences are saved in this browser.",
  显示名称: "Display name",
  邮箱: "Email",
  账号与工作空间: "Account & workspace",
  未提供: "Not provided",
  "Hosted 身份在 NexusDock 管理；模型授权不属于登录身份。":
    "Manage your hosted identity in NexusDock; model authorization is separate.",
  需要所有者权限: "Owner access required",
  "这些设置只允许 Workspace owner 修改。":
    "Only workspace owners can change these settings.",
  未找到设置页面: "Settings page not found",
  "请选择左侧的设置分类。": "Choose a category from the navigation.",
  "没有项目，请先创建项目。": "No projects yet. Create one first.",
  选择项目: "Select project",
  名称: "Name",
  描述: "Description",
  "Project Agent Instructions": "Project Agent Instructions",
  "此项目的长期 Agent 工作指令":
    "Long-term Agent instructions for this project",
  保存项目: "Save project",
  项目已保存: "Project saved",
  "Project Instructions 只作用于选中的项目，不影响整个工作空间。":
    "Project instructions apply only to the selected project, not the whole workspace.",
  "从一个想法开始，与 Agent 讨论，再把方向变成任务。":
    "Start with an idea, discuss it with an Agent, and turn it into actionable work.",
  新建讨论: "New discussion",
  讨论列表: "Discussions",
  没有匹配的讨论: "No matching discussions",
  打开讨论列表: "Open discussions",
  "今天想推进什么？": "What would you like to move forward today?",
  "直接输入第一句话，CycleDock 会创建 Discussion 并开始协作。":
    "Send your first message and CycleDock will create a Discussion and start collaborating.",
  "发送消息，开始讨论…": "Send a message to start a discussion…",
  还没有项目: "No projects yet",
  "请先在看板中创建一个项目。": "Create a project from the Board first.",
  "读取项目…": "Loading projects…",
  项目: "Project",
  "搜索讨论…": "Search discussions…",
  "读取讨论…": "Loading discussions…",
  还没有讨论: "No discussions yet",
  "新建一个讨论，直接发送第一条消息。":
    "Create a discussion and send your first message.",
  开始讨论: "Start a discussion",
  新讨论: "New discussion",
  "继续对话，探索想法…": "Continue the conversation…",
  已归档: "Archived",
  讨论中: "Active",
  "还没有项目，请先在看板中创建一个项目。":
    "No projects yet. Create one from the Board first.",
  前往看板: "Go to Board",
  创建讨论失败: "Failed to create discussion",
  "把讨论变成工作。": "Turn discussions into work.",
  "让进展清晰可见。": "Make progress visible.",
  "统一的对话时间线、看板、持续工作与 AI Agent。":
    "A shared conversation timeline, boards, ongoing work and AI Agents.",
  工作空间权限由服务端维护: "Workspace permissions are enforced by the server",
  "登录 CycleDock": "Sign in to CycleDock",
  继续进入你的工作空间: "Continue to your workspace",
  使用: "Continue with",
  登录: "Sign in",
  管理员密码: "Administrator password",
  输入管理员密码: "Enter administrator password",
  登录失败: "Sign-in failed",
  "Hosted 使用 NexusDock 统一认证；Self-hosted 可使用管理员密码。":
    "Hosted uses NexusDock SSO; self-hosted can use an administrator password.",
  "正在验证会话…": "Verifying session…",
  "正在加载会话…": "Loading conversation…",
  待办: "To do",
  进行中: "Working",
  待审核: "In review",
  已完成: "Done",
  "PROJECT BOARD": "PROJECT BOARD",
  DISCUSSIONS: "DISCUSSIONS",
  "查看执行状态，跟踪长期工作。": "Track tasks, progress and ongoing work.",
  新建项目: "New project",
  新建待办: "New to-do",
  待办标题: "To-do title",
  待办描述: "To-do description",
  "要完成什么？": "What needs to be done?",
  "补充目标、范围或完成条件": "Add goals, scope, or completion criteria",
  创建待办: "Create to-do",
  切换项目: "Switch project",
  从讨论中生成工作: "Generate work from discussions",
  没有任务: "No tasks",
  持续事项: "Ongoing",
  暂无持续事项: "No ongoing work",
  未运行: "Not running",
  项目名称: "Project name",
  项目描述: "Project description",
  创建项目: "Create project",
  移动失败: "Could not move card",
  任务详情: "Task details",
  返回: "Back",
  生成任务: "Generate task",
  正在执行: "Running",
  排队中: "Queued",
  等待回复: "Waiting for reply",
  运行中: "Running",
  发送: "Send",
  加入队列: "Queue",
  立即转向: "Interrupt",
  回复并继续: "Reply and continue",
  "正在连接实时通道…": "Connecting to live updates…",
  "此讨论已归档，不能继续发送消息": "This discussion is archived",
  讨论已归档: "Archived discussion",
  "回复 Agent，继续执行…": "Reply to the Agent to continue…",
  "发送消息，继续推进任务…": "Send a message to keep work moving…",
  执行失败: "Run failed",
  生成任务指令已提交: "Task generation requested",
  从第一句话开始: "Start with your first message",
  开始协作: "Start collaborating",
  "你的消息、Agent 结果和任务状态都会出现在这里。":
    "Your messages, Agent results and status updates appear here.",
  跳到最新: "Jump to latest",
  正在生成回复: "Generating a reply",
  阶段进度: "Progress",
  巡检: "Check-in",
  状态变化: "Status update",
  提交审核: "Submitted for review",
  驳回: "Rejected",
  发现: "Finding",
  结果: "Result",
  "NexusDock 远程设备": "NexusDock remote devices",
  模型提供商: "Model provider",
  "内置 Agent": "Built-in Agent",
  主导航: "Main navigation",
  发送失败: "Failed to send",
  任务发送方式: "Dispatch mode",
  取消执行: "Cancel run",
  等待人工回复: "Waiting for human reply",
  统一消息时间线: "Conversation timeline",
  复制代码: "Copy code",
  "Agent 正在工作": "Agent is working",
  "保存重要事件与结果，不保存模型内部思维链或原始工具记录。":
    "CycleDock keeps key events and results, not private reasoning or raw tool traces.",
  "Agent 配置已保存": "Agent settings saved",
  "这些配置作用于整个 Workspace；具体模型连接请前往「模型服务」。":
    "These settings apply to your workspace; manage providers under Model providers.",
  "启用 Cycle Agent": "Enable Cycle Agent",
  "请求超时（毫秒）": "Request timeout (ms)",
  最大工具轮次: "Maximum tool rounds",
  "整个工作空间的默认指令，与单个项目的指令分开保存。":
    "Workspace-wide instructions; project instructions are saved separately.",
  "保存 Agent 设置": "Save Agent settings",

  "语言与外观仅保存在当前浏览器中。":
    "Language and appearance are saved only in this browser.",
  未设置: "Not set",
  当前工作空间: "Current workspace",
  切换工作空间: "Switch workspace",
  "Hosted 身份在 NexusDock 管理；模型账号授权与登录身份相互独立。":
    "Hosted accounts are managed in NexusDock. Model authorization is separate from sign-in.",
  "NexusDock 设备授权": "NexusDock device access",
  "当前环境未启用第一方 NexusDock 连接。":
    "First-party NexusDock access is unavailable in this environment.",
  "已授权此工作空间访问你的 NexusDock 设备。":
    "This workspace is authorized to access your NexusDock devices.",
  "尚未授权设备访问。": "Device access has not been authorized.",
  "连接 NexusDock": "Connect NexusDock",
  断开连接: "Disconnect",
  查看设备: "View devices",
  "这些设置仅允许 Workspace owner 管理。":
    "Only workspace owners can manage these settings.",
  没有这个设置页面: "Settings page not found",
  "请从设置导航中选择有效分类。": "Choose a valid settings category.",
  设置分类: "Settings categories",
  选择设置类别: "Choose a settings category",
  返回工作台: "Back to workspace",
  "模型授权属于 Agent 配置，不会替代 CycleDock 登录身份。":
    "Model authorization belongs to Agent settings and does not replace CycleDock sign-in.",
  已连接: "Connected",
  未连接: "Not connected",
  断开授权: "Revoke access",
  开始授权: "Authorize",
  授权已完成: "Authorization complete",
  "Grok 已连接": "Grok connected",
  "授权尚未完成，请在 Grok 页面继续操作":
    "Authorization is pending. Continue in the Grok page.",
  "此环境需要手动完成授权回跳。在新窗口登录后，将浏览器跳转后的完整地址粘贴在下方。":
    "Manual callback required. After signing in in a new window, paste the full redirected URL below.",
  "打开 ChatGPT 授权页面": "Open ChatGPT authorization",
  "授权回跳 URL": "Callback URL",
  "粘贴完整的回跳 URL": "Paste the full callback URL",
  完成授权: "Complete authorization",
  取消: "Cancel",
  "在授权页面输入代码：": "Enter this code on the authorization page:",
  打开授权页面: "Open authorization page",
  检查授权结果: "Check authorization",
  "OpenAI-compatible 模型请在「Agent 设置」中配置 API 地址和环境变量名，凭据不直接写入表单或数据库。":
    "For OpenAI-compatible models, configure the API endpoint and environment variable name under Agent settings. Credentials are never saved in the form or database.",
  "启用内置 Agent": "Enable built-in Agent",
  模型来源: "Model provider",
  "API Key 环境变量名": "API key environment variable",
  "模型 ID": "Model ID",
  "输入已授权可用的模型 ID": "Enter an available authorized model ID",
  "请先在「模型服务」完成此平台授权，再读取当前账号的模型目录。":
    "Authorize this provider in Model providers to load the account's model catalog.",
  "正在读取模型目录…": "Loading model catalog…",
  "模型目录暂不可用，可保留手动输入的模型 ID。":
    "Model catalog unavailable. You can keep a manually entered model ID.",
  已授权模型目录: "Authorized model catalog",
  从当前授权账号选择模型: "Choose a model from the connected account",
  最大工具调用轮数: "Maximum tool rounds",
  "保存 Agent 配置": "Save Agent settings",
  测试当前模型: "Test selected model",
  请选择模型: "Choose a model",
  "已保存的 MCP": "Saved MCP connections",
  "尚未添加外部 MCP 连接。": "No external MCP connections yet.",
  编辑: "Edit",
  "编辑 MCP": "Edit MCP connection",
  "新建 MCP": "New MCP connection",
  请输入名称: "Enter a name",
  "请输入有效 URL": "Enter a valid URL",
  "Bearer Token 环境变量名（可选）":
    "Bearer token environment variable (optional)",
  "超时（毫秒）": "Timeout (ms)",
  启用此连接: "Enable this connection",
  保存修改: "Save changes",
  添加连接: "Add connection",
  测试连接: "Test connection",
  取消编辑: "Cancel editing",
  "确定删除这个 MCP 连接吗？": "Delete this MCP connection?",
  删除: "Delete",
  "MCP 连接已保存": "MCP connection saved",
  项目设置已保存: "Project settings saved",
  请输入项目名称: "Enter a project name",
  "没有项目，请先在工作台创建项目。":
    "No projects yet. Create a project from the workspace.",
  设置项目: "Project to edit",
  "Project Instructions 仅作用于此项目，不影响其他项目。":
    "Project instructions apply only to this project.",
  "通过 ChatGPT 授权": "Authorize with ChatGPT",
  "通过 Grok 授权": "Authorize with Grok",
  "项目创建后仍停留在当前讨论或工作模式。":
    "The new project opens in your current Discussion or Work view.",
  "项目简介（可选）": "Project description (optional)",
  创建: "Create",
  "例如：产品规划": "For example: Product planning",
  项目目标与范围: "Project goals and scope",
  项目已创建: "Project created",
  " · 当前": " · Current",
  搜索讨论: "Search discussions",
  无匹配的讨论: "No matching discussions",
  未命名讨论: "Untitled discussion",
  "创建第一个项目，开始讨论或添加待办。":
    "Create your first project to start discussions or tasks.",
  项目不可访问: "Project unavailable",
  "该项目不存在，或当前工作空间无访问权限。":
    "This project does not exist or you lack access in the current workspace.",
  列表加载失败: "Failed to load list",
  工作导航: "Work navigation",
  "选择项目、讨论和工作": "Choose a project, discussion, or work view.",
  "先和 Agent 讨论需求，明确目标后再生成待办或持续事项。":
    "Discuss your goals with an Agent, then generate Todo or Ongoing work.",
  请先输入讨论内容: "Enter your discussion first",
  "未能发送，草稿已保留，可重试：": "Could not send; your draft is preserved: ",
  新讨论内容: "New discussion message",
  "描述问题、想法或准备推进的工作…": "Describe an idea, problem, or task…",
  "首次发送尚未完成，重试将继续同一讨论":
    "The first message did not send. Retry in the same discussion.",
  "Enter 发送 · Shift+Enter 换行": "Enter to send · Shift+Enter for a new line",
  "发送失败，内容与目标 Discussion 已保留。":
    "Sending failed. Your message and draft discussion have been preserved.",
  最近讨论: "Recent discussions",
  "发送上面的第一条消息即可开始。": "Send the first message above to begin.",
  暂无任务: "No tasks yet",
  "独立创建的待办不需要来源 Discussion。":
    "You can create a Todo independently, without a source discussion.",
  标题: "Title",
  任务说明: "Task description",
  待办看板: "Todo board",
  拖动至目标状态: "Drag to the target status",
  "任务状态变更失败：": "Could not change task status: ",
  已暂停: "Paused",
  尚无检查: "No check-ins yet",
  健康: "Healthy",
  警告: "Warning",
  严重: "Critical",
  "可从讨论生成长期或周期工作。":
    "Generate recurring or ongoing work from a discussion.",
  说明: "Description",
  来源讨论: "Source discussion",
  打开原讨论: "Open source discussion",
  编辑信息: "Edit details",
  卡片信息: "Card information",
  查看或编辑卡片信息: "View or edit card information",
  归档讨论: "Archive discussion",
  删除讨论: "Delete discussion",
  "此讨论已归档，无法继续发送消息。":
    "This discussion is archived and cannot receive messages.",
  无法打开卡片: "Unable to open card",
  卡片不可用: "Card unavailable",
  暂无说明: "No description",
  已保存: "Saved",
  讨论操作: "Discussion actions",
  "删除此讨论？关联工作的卡片仍保留。":
    "Delete this discussion? Related work cards will remain.",
  "发送新指令或评论…": "Send a new instruction or comment…",
  新指令处理方式: "How to handle this instruction",
  取消当前运行: "Cancel current run",
  执行中: "Running",
  回复内容: "Reply",
  卡片状态: "Card status",
  取消排队: "Cancel queued work",
  重试: "Retry",
  "尚无消息，发送第一条内容开始协作。":
    "No messages yet. Send one to start collaborating.",
  进度: "Progress",
  检查: "Check-in",
  确认: "Confirm",
  退回: "Return for changes",
  执行取消: "Run cancelled",
  执行已转向: "Run superseded",
  执行完成: "Run completed",
  执行过期: "Run expired",
  "Agent 正在处理": "Agent is working",
  定时计划: "Schedules",
  暂无定时计划: "No schedules yet",
  新增日程: "Add schedule",
  编辑日程: "Edit schedule",
  周期: "Frequency",
  每天: "Daily",
  每周: "Weekly",
  每月: "Monthly",
  星期: "Weekday",
  "每月日期（1–28）": "Day of month (1–28)",
  小时: "Hour",
  分钟: "Minute",
  时区: "Time zone",
  启用计划: "Enable schedule",
  最近检查: "Latest check-in",
  必须填写时区: "Time zone is required",
  "请输入有效 IANA 时区": "Enter a valid IANA time zone",
  周日: "Sunday",
  周一: "Monday",
  周二: "Tuesday",
  周三: "Wednesday",
  周四: "Thursday",
  周五: "Friday",
  周六: "Saturday",
  日: "day",
  计划已删除: "Schedule deleted",
  定时计划已保存: "Schedule saved",
  "· 已禁用": "· Disabled",
  删除定时计划: "Delete schedule",
  "确认删除此计划？如果已生成过历史触发，服务端可能拒绝删除；届时请编辑并禁用计划。":
    "Delete this schedule? If it already produced triggers, the server may refuse; disable it instead.",
  确认删除: "Confirm deletion",
  计划周期: "Schedule frequency",
  保存日程: "Save schedule",
  登录工作空间: "Sign in to workspace",
  "登录后继续管理讨论和工作。": "Sign in to manage discussions and tasks.",
  "当前未配置登录方式。": "No sign-in methods configured.",
  无法读取可用登录方式: "Could not load sign-in methods",
  正在加载讨论: "Loading discussion",
  正在加载侧栏: "Loading sidebar",
  正在加载消息: "Loading messages",
  正在加载看板: "Loading board",
  正在加载卡片: "Loading card",
  正在加载设置: "Loading settings",
  工作模式: "Work mode",
} as const;

void i18n.use(initReactI18next).init({
  lng: getStoredLanguage(),
  fallbackLng: "zh",
  resources: {
    zh: { translation: {} },
    en: { translation: en },
  },
  interpolation: { escapeValue: false },
  initAsync: false,
});

export function setLanguage(language: Language) {
  try {
    localStorage.setItem(LANGUAGE_KEY, language);
  } catch {
    /* browsing without storage */
  }
  void i18n.changeLanguage(language);
  document.documentElement.lang = language === "zh" ? "zh-CN" : "en";
}

export function initializeLanguage() {
  setLanguage(getStoredLanguage());
}

export { i18n };
