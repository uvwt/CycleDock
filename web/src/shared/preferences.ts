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
  开启卡片更新通知: "Enable card update notifications",
  "离开卡片或切到后台，也能及时看到模型回复与卡片更新。":
    "See model replies and card updates when viewing another card or working in the background.",
  暂不开启: "Not now",
  浏览器通知: "Browser notifications",
  开启通知: "Enable notifications",
  关闭通知: "Disable notifications",
  卡片有更新: "Card updated",
  "卡片有更新，点击查看": "Card updated. Click to view.",
  浏览器通知启动失败: "Failed to start browser notifications",
  "切到后台或离开当前卡片后，卡片有更新时通知。网页需保持打开。":
    "Notify on card updates when in the background or viewing another card. Keep the page open.",
  "当前环境不支持浏览器通知，请使用 HTTPS 或本机浏览器。":
    "Notifications are unavailable. Use HTTPS or localhost in a supported browser.",
  "通知权限已被阻止，请在浏览器的网站设置中允许通知。":
    "Notifications are blocked. Allow them in your browser's site settings.",
  聊天: "Chat",
  看板: "Board",
  工作: "Work",
  账户与工作空间: "Account & workspace",
  已切换工作空间: "Workspace switched",
  工作空间切换失败: "Failed to switch workspace",
  工作空间加载失败: "Failed to load workspaces",
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
  工作列表: "Work list",
  打开工作列表: "Open work list",
  "今天想推进什么？": "What would you like to move forward today?",
  "发送消息，开始讨论…": "Send a message to start a discussion…",
  还没有项目: "No projects yet",
  "请先在看板中创建一个项目。": "Create a project from the Board first.",
  "请先在工作区中创建一个项目。": "Create a project from Work first.",
  前往工作: "Go to Work",
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
  "创建一个新的项目看板来组织任务与 Agent 会话。":
    "Create a new project board to organize tasks and Agent sessions.",
  输入项目名称: "Enter project name",
  描述该项目的工作范围或目标: "Describe the project scope or goals",
  新建待办: "New to-do",
  "添加一个待办任务到当前项目看板中。": "Add a to-do task to this board.",
  取消: "Cancel",
  待办标题: "To-do title",
  待办描述: "To-do description",
  "要完成什么？": "What needs to be done?",
  "补充目标、范围或完成条件": "Add goals, scope, or completion criteria",
  创建待办: "Create to-do",
  切换项目: "Switch project",
  从讨论中生成工作: "Generate work from discussions",
  没有任务: "No tasks",
  计划: "Plans",
  计划列表: "Plan list",
  "Agent 列表": "Agent list",
  活动列表: "Activity list",
  全部来源: "All sources",
  打开计划列表: "Open plan list",
  全部计划: "All plans",
  未命名计划: "Untitled plan",
  已暂停: "Paused",
  健康状态: "Health",
  日程已停用: "Schedule disabled",
  未设置自动执行时间: "No automatic schedule",
  "管理长期工作，查看执行结果与自动执行日程。":
    "Manage ongoing work, results and automatic schedules.",
  "查看任务执行状态，跟踪待办进度。": "Track task status and to-do progress.",
  这个项目还没有计划: "No plans in this project yet",
  这个筛选下没有计划: "No plans match this filter",
  "从讨论中生成长期工作，在详情中配置自动执行时间。":
    "Generate ongoing work from a discussion and configure its schedule in the details.",
  未命名任务: "Untitled task",
  该状态暂无任务: "No tasks in this state",
  未运行: "Not running",
  项目名称: "Project name",
  项目描述: "Project description",
  创建项目: "Create project",
  移动失败: "Could not move card",
  任务详情: "Task details",
  任务信息: "Task info",
  讨论信息: "Discussion info",
  切换任务状态: "Change task status",
  状态更新失败: "Failed to update status",
  请先提交待审核: "Move to review first",
  返回: "Back",
  生成任务: "Generate task",
  正在执行: "Running",
  排队中: "Queued",
  等待回复: "Waiting for reply",
  运行中: "Running",
  发送: "Send",
  加入队列: "Queue",
  回复并继续: "Reply and continue",
  已收到回复: "Reply received",
  执行完成: "Completed",
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
  提交失败: "Failed to submit",
  取消失败: "Failed to cancel",
  取消排队失败: "Failed to cancel queued work",
  重试失败: "Retry failed",
  取消排队: "Cancel queued work",
  重试: "Retry",
  "系统触发 / 未关联历史": "System-triggered / unlinked history",
  待执行队列: "Pending queue",
  引导: "Steer",
  引导失败: "Could not steer queued input",
  "立即执行这条排队输入，替代当前执行":
    "Run this queued input next, replacing the current execution",
  排队输入: "Queued input",
  编辑内容: "Edit content",
  "新讨论发送失败，草稿已保留":
    "Failed to send the new discussion. Draft saved.",
  "重试失败，草稿仍已保留": "Retry failed. Draft remains saved.",
  "讨论已创建，但首条消息还未确认送达。重试会复用同一 Card，不会重复创建讨论。":
    "The discussion was created, but the first message is not confirmed. Retry reuses the same Card and will not create a duplicate.",
  打开讨论: "Open discussion",
  重试发送: "Retry send",
  中止: "Stop",
  等待人工回复: "Waiting for human reply",
  统一消息时间线: "Conversation timeline",
  复制代码: "Copy code",
  "Agent 正在工作": "Agent is working",
  "保存重要事件与结果，不保存模型内部思维链或原始工具记录。":
    "CycleDock keeps key events and results, not private reasoning or raw tool traces.",
  "Agent 配置已保存": "Agent settings saved",
  "这些配置作用于整个 Workspace；具体模型连接请前往「模型服务」。":
    "These settings apply to your workspace; manage providers under Model providers.",
  "整个工作空间的默认指令，与单个项目的指令分开保存。":
    "Workspace-wide instructions; project instructions are saved separately.",
  "保存 Agent 设置": "Save Agent settings",
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
