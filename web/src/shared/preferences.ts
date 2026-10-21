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
