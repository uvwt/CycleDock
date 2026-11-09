package modelclient

import (
	"fmt"
	"strings"
	"time"
)

const (
	LimitsSourceUser     = "user"
	LimitsSourceProvider = "provider"
	LimitsSourceRegistry = "registry"
	LimitsSourceUnknown  = "unknown"

	maxReasonableTokenLimit = 10_000_000
)

// ModelLimits 描述模型路由的上下文容量与输入/输出上限。
// 0 表示未知，不得自动解释为某个固定默认容量。
type ModelLimits struct {
	ContextWindowTokens int        `json:"context_window_tokens,omitempty"`
	MaxInputTokens      int        `json:"max_input_tokens,omitempty"`
	MaxOutputTokens     int        `json:"max_output_tokens,omitempty"`
	Source              string     `json:"source"`
	VerifiedAt          *time.Time `json:"verified_at,omitempty"`
}

func (m ModelLimits) HasKnownCapacity() bool {
	return m.ContextWindowTokens > 0 || m.MaxInputTokens > 0 || m.MaxOutputTokens > 0
}

func ValidLimitsSource(source string) bool {
	switch strings.TrimSpace(source) {
	case LimitsSourceUser, LimitsSourceProvider, LimitsSourceRegistry, LimitsSourceUnknown:
		return true
	default:
		return false
	}
}

// ValidateUserModelLimits 校验用户配置或持久化的模型容量参数。
func ValidateUserModelLimits(limits ModelLimits) (ModelLimits, error) {
	if limits.ContextWindowTokens < 0 || limits.MaxInputTokens < 0 || limits.MaxOutputTokens < 0 {
		return ModelLimits{}, fmt.Errorf("model token limits must be positive integers")
	}
	if limits.ContextWindowTokens > maxReasonableTokenLimit ||
		limits.MaxInputTokens > maxReasonableTokenLimit ||
		limits.MaxOutputTokens > maxReasonableTokenLimit {
		return ModelLimits{}, fmt.Errorf("model token limits exceed maximum supported bound (%d)", maxReasonableTokenLimit)
	}
	if limits.ContextWindowTokens > 0 {
		if limits.MaxInputTokens > limits.ContextWindowTokens {
			return ModelLimits{}, fmt.Errorf("max_input_tokens cannot exceed context_window_tokens")
		}
		if limits.MaxOutputTokens > limits.ContextWindowTokens {
			return ModelLimits{}, fmt.Errorf("max_output_tokens cannot exceed context_window_tokens")
		}
	}
	source := strings.TrimSpace(limits.Source)
	if source == "" {
		if limits.HasKnownCapacity() {
			source = LimitsSourceUser
		} else {
			source = LimitsSourceUnknown
		}
	}
	if !ValidLimitsSource(source) {
		return ModelLimits{}, fmt.Errorf("unsupported model limits source %q", source)
	}
	if source == LimitsSourceUser && !limits.HasKnownCapacity() {
		source = LimitsSourceUnknown
	}
	limits.Source = source
	return limits, nil
}

type registryEntry struct {
	contextWindow int
	maxInput      int
	maxOutput     int
}

// curatedModelRegistry 维护主流官方模型的精确容量元数据（对齐 models.dev 与官方最新目录）。
// 仅对精确识别的模型 ID（或用户显式指定的 base_model）生效，不对未知自定义别名做模糊猜测。
var curatedModelRegistry = map[string]registryEntry{
	// OpenAI GPT-6 / GPT-5.6 / GPT-5.5 / GPT-5.4 / GPT-5 / Codex / o-series
	"gpt-6.1-terra":            {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-6.1-terra-pro":        {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-6.1-sol":              {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-6.1-sol-pro":          {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-6-astra":              {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-6-astra-pro":          {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-6-luna":               {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-6-luna-pro":           {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-6-sol":                {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-6-sol-pro":            {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-5.6":                  {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-5.6-luna":             {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-5.6-luna-pro":         {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-5.6-sol":              {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-5.6-sol-pro":          {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-5.6-terra":            {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-5.6-terra-pro":        {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-5.5":                  {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-5.5-pro":              {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-5.4":                  {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-5.4-pro":              {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-5.4-mini":             {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-5.4-nano":             {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-5.3-codex":            {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-5.3-codex-spark":      {contextWindow: 128000, maxInput: 100000, maxOutput: 32000},
	"gpt-5.2":                  {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-5.2-pro":              {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-5.2-codex":            {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-5.2-chat":             {contextWindow: 128000, maxInput: 128000, maxOutput: 32000},
	"gpt-5.1":                  {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-5.1-codex":            {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-5.1-codex-max":        {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-5.1-codex-mini":       {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-5":                    {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-5-pro":                {contextWindow: 400000, maxInput: 272000, maxOutput: 272000},
	"gpt-5-mini":               {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-5-nano":               {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-5-codex":              {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-daybreak-blue-latest": {contextWindow: 1050000, maxInput: 922000, maxOutput: 128000},
	"gpt-daybreak-red-latest":  {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-chat-latest":          {contextWindow: 400000, maxInput: 272000, maxOutput: 128000},
	"gpt-oss-120b":             {contextWindow: 131072, maxInput: 131072, maxOutput: 65536},
	"gpt-oss-20b":              {contextWindow: 131072, maxInput: 131072, maxOutput: 65536},
	"gpt-4.1":                  {contextWindow: 1047576, maxInput: 1047576, maxOutput: 32768},
	"gpt-4.1-mini":             {contextWindow: 1047576, maxInput: 1047576, maxOutput: 32768},
	"gpt-4.1-nano":             {contextWindow: 1047576, maxInput: 1047576, maxOutput: 32768},
	"gpt-4o":                   {contextWindow: 128000, maxInput: 128000, maxOutput: 16384},
	"gpt-4o-2024-11-20":        {contextWindow: 128000, maxInput: 128000, maxOutput: 16384},
	"gpt-4o-2024-08-06":        {contextWindow: 128000, maxInput: 128000, maxOutput: 16384},
	"gpt-4o-mini":              {contextWindow: 128000, maxInput: 128000, maxOutput: 16384},
	"gpt-4o-mini-2024-07-18":   {contextWindow: 128000, maxInput: 128000, maxOutput: 16384},
	"o4-mini":                  {contextWindow: 200000, maxInput: 200000, maxOutput: 100000},
	"o3":                       {contextWindow: 200000, maxInput: 200000, maxOutput: 100000},
	"o3-pro":                   {contextWindow: 200000, maxInput: 200000, maxOutput: 100000},
	"o3-mini":                  {contextWindow: 200000, maxInput: 200000, maxOutput: 100000},
	"o1":                       {contextWindow: 200000, maxInput: 200000, maxOutput: 100000},
	"o1-pro":                   {contextWindow: 200000, maxInput: 200000, maxOutput: 100000},
	"o1-mini":                  {contextWindow: 128000, maxInput: 128000, maxOutput: 65536},

	// Anthropic Claude 5.5 / 5 / 4.8 / 4.7 / 4.6 / 4.5 (同时支持连字符与点号写法)
	"claude-opus-5-5":            {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-opus-5.5":            {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-opus-5":              {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-sonnet-5-5":          {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-sonnet-5.5":          {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-sonnet-5":            {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-haiku-5-5":           {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-haiku-5.5":           {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-fable-5-1":           {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-fable-5.1":           {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-fable-5":             {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-opus-4-8":            {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-opus-4.8":            {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-opus-4-7":            {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-opus-4.7":            {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-opus-4-6":            {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-opus-4.6":            {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-sonnet-4-6":          {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-sonnet-4.6":          {contextWindow: 1000000, maxInput: 1000000, maxOutput: 128000},
	"claude-opus-4-5":            {contextWindow: 200000, maxInput: 200000, maxOutput: 64000},
	"claude-opus-4.5":            {contextWindow: 200000, maxInput: 200000, maxOutput: 64000},
	"claude-opus-4-5-20251101":   {contextWindow: 200000, maxInput: 200000, maxOutput: 64000},
	"claude-sonnet-4-5":          {contextWindow: 200000, maxInput: 200000, maxOutput: 64000},
	"claude-sonnet-4.5":          {contextWindow: 200000, maxInput: 200000, maxOutput: 64000},
	"claude-sonnet-4-5-20250929": {contextWindow: 200000, maxInput: 200000, maxOutput: 64000},
	"claude-haiku-4-5":           {contextWindow: 200000, maxInput: 200000, maxOutput: 64000},
	"claude-haiku-4.5":           {contextWindow: 200000, maxInput: 200000, maxOutput: 64000},
	"claude-haiku-4-5-20251001":  {contextWindow: 200000, maxInput: 200000, maxOutput: 64000},
	"claude-opus-4-1":            {contextWindow: 200000, maxInput: 200000, maxOutput: 32000},
	"claude-opus-4.1":            {contextWindow: 200000, maxInput: 200000, maxOutput: 32000},
	"claude-opus-4-0":            {contextWindow: 200000, maxInput: 200000, maxOutput: 32000},
	"claude-sonnet-4-0":          {contextWindow: 200000, maxInput: 200000, maxOutput: 64000},
	"claude-3-7-sonnet-latest":   {contextWindow: 200000, maxInput: 200000, maxOutput: 64000},
	"claude-3-7-sonnet-20250219": {contextWindow: 200000, maxInput: 200000, maxOutput: 64000},
	"claude-3-5-sonnet-latest":   {contextWindow: 200000, maxInput: 200000, maxOutput: 8192},
	"claude-3-5-sonnet-20241022": {contextWindow: 200000, maxInput: 200000, maxOutput: 8192},
	"claude-3-5-haiku-latest":    {contextWindow: 200000, maxInput: 200000, maxOutput: 8192},
	"claude-3-5-haiku-20241022":  {contextWindow: 200000, maxInput: 200000, maxOutput: 8192},

	// Google Gemini 4 / 3.8 / 3.7 / 3.6 / 3.5 / 3.1 / 3 / 2.5 & Gemma 4
	"gemini-4-argon":                     {contextWindow: 1048576, maxInput: 1048576, maxOutput: 32768},
	"gemini-3.8-pro":                     {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.8-flash":                   {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.8-flash-lite":              {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.7-pro":                     {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.7-flash":                   {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.6-pro":                     {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.6-flash":                   {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.5-pro":                     {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.5-flash":                   {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.5-flash-lite":              {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.1-pro":                     {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.1-pro-preview":             {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.1-pro-preview-customtools": {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.1-flash":                   {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.1-flash-lite":              {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3.1-flash-lite-preview":      {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3-pro":                       {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3-pro-preview":               {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3-flash":                     {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-3-flash-preview":             {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-pro-latest":                  {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-flash-latest":                {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-flash-lite-latest":           {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-2.5-pro":                     {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-2.5-pro-preview":             {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-2.5-flash":                   {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-2.5-flash-lite":              {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"gemini-2.0-flash":                   {contextWindow: 1048576, maxInput: 1048576, maxOutput: 8192},
	"gemini-2.0-flash-lite":              {contextWindow: 1048576, maxInput: 1048576, maxOutput: 8192},
	"gemini-1.5-pro":                     {contextWindow: 2097152, maxInput: 2097152, maxOutput: 8192},
	"gemini-1.5-flash":                   {contextWindow: 1048576, maxInput: 1048576, maxOutput: 8192},
	"gemma-4-31b-it":                     {contextWindow: 262144, maxInput: 262144, maxOutput: 32768},
	"gemma-4-26b-a4b-it":                 {contextWindow: 262144, maxInput: 262144, maxOutput: 32768},

	// DeepSeek V4.1 / V4 / V3.2 / V3.1 / R1
	"deepseek-v4.1-flash":          {contextWindow: 1048576, maxInput: 1048576, maxOutput: 393216},
	"deepseek-v4.1-flash-1017":     {contextWindow: 1048576, maxInput: 1048576, maxOutput: 393216},
	"deepseek-v4-pro":              {contextWindow: 1048576, maxInput: 1048576, maxOutput: 393216},
	"deepseek-v4-pro-0813":         {contextWindow: 1048576, maxInput: 1048576, maxOutput: 393216},
	"deepseek-v4-flash":            {contextWindow: 1048576, maxInput: 1048576, maxOutput: 393216},
	"deepseek-v4-flash-0731":       {contextWindow: 1000000, maxInput: 1000000, maxOutput: 384000},
	"deepseek-v4-flash-vision-exp": {contextWindow: 1048576, maxInput: 1048576, maxOutput: 393216},
	"deepseek-flash":               {contextWindow: 1048576, maxInput: 1048576, maxOutput: 393216},
	"deepseek-v3.2":                {contextWindow: 163840, maxInput: 163840, maxOutput: 147456},
	"deepseek-v3.2-exp":            {contextWindow: 163840, maxInput: 163840, maxOutput: 147456},
	"deepseek-v3.1":                {contextWindow: 163840, maxInput: 163840, maxOutput: 147456},
	"deepseek-v3.1-terminus":       {contextWindow: 163840, maxInput: 163840, maxOutput: 147456},
	"deepseek-chat-v3.1":           {contextWindow: 163840, maxInput: 163840, maxOutput: 147456},
	"deepseek-chat-v3-0324":        {contextWindow: 163840, maxInput: 163840, maxOutput: 115200},
	"deepseek-r1-0528":             {contextWindow: 163840, maxInput: 163840, maxOutput: 32768},
	"deepseek-chat":                {contextWindow: 163840, maxInput: 163840, maxOutput: 16384},
	"deepseek-reasoner":            {contextWindow: 163840, maxInput: 163840, maxOutput: 64000},
	"deepseek-v3":                  {contextWindow: 128000, maxInput: 128000, maxOutput: 8192},
	"deepseek-r1":                  {contextWindow: 128000, maxInput: 128000, maxOutput: 64000},

	// xAI Grok 4.7 / 4.6 / 4.5 / 4.3 / 4.20 / 4.1 / 4 / 3
	"grok-4.7":                     {contextWindow: 500000, maxInput: 500000, maxOutput: 500000},
	"grok-4.6":                     {contextWindow: 500000, maxInput: 500000, maxOutput: 500000},
	"grok-4.5":                     {contextWindow: 500000, maxInput: 500000, maxOutput: 500000},
	"grok-4.3":                     {contextWindow: 1000000, maxInput: 1000000, maxOutput: 30000},
	"grok-4.20":                    {contextWindow: 1000000, maxInput: 1000000, maxOutput: 30000},
	"grok-4.20-0309-reasoning":     {contextWindow: 1000000, maxInput: 1000000, maxOutput: 30000},
	"grok-4.20-0309-non-reasoning": {contextWindow: 1000000, maxInput: 1000000, maxOutput: 30000},
	"grok-4.20-multi-agent":        {contextWindow: 1000000, maxInput: 1000000, maxOutput: 30000},
	"grok-4.20-multi-agent-0309":   {contextWindow: 1000000, maxInput: 1000000, maxOutput: 30000},
	"grok-4.1":                     {contextWindow: 2000000, maxInput: 2000000, maxOutput: 64000},
	"grok-4.1-fast":                {contextWindow: 2000000, maxInput: 2000000, maxOutput: 64000},
	"grok-4-fast":                  {contextWindow: 2000000, maxInput: 2000000, maxOutput: 64000},
	"grok-4":                       {contextWindow: 256000, maxInput: 256000, maxOutput: 64000},
	"grok-build-0.1":               {contextWindow: 256000, maxInput: 256000, maxOutput: 256000},
	"grok-code-fast-1":             {contextWindow: 256000, maxInput: 256000, maxOutput: 64000},
	"grok-3":                       {contextWindow: 131072, maxInput: 131072, maxOutput: 32768},
	"grok-3-mini":                  {contextWindow: 131072, maxInput: 131072, maxOutput: 32768},
	"grok-3-fast":                  {contextWindow: 131072, maxInput: 131072, maxOutput: 32768},
	"grok-3-mini-fast":             {contextWindow: 131072, maxInput: 131072, maxOutput: 32768},
	"grok-2":                       {contextWindow: 131072, maxInput: 131072, maxOutput: 32768},
	"grok-2-latest":                {contextWindow: 131072, maxInput: 131072, maxOutput: 32768},

	// Alibaba Qwen 3.8 / 3.7 / 3.6 / 3.5 / 3
	"qwen3.8-max":                  {contextWindow: 1000000, maxInput: 1000000, maxOutput: 131072},
	"qwen3.8-max-0902":             {contextWindow: 1000000, maxInput: 1000000, maxOutput: 131072},
	"qwen3.8-max-prime":            {contextWindow: 1000000, maxInput: 1000000, maxOutput: 131072},
	"qwen3.8-plus":                 {contextWindow: 1000000, maxInput: 1000000, maxOutput: 131072},
	"qwen3.8-flash":                {contextWindow: 1000000, maxInput: 1000000, maxOutput: 131072},
	"qwen3.8-omni-flash":           {contextWindow: 1000000, maxInput: 1000000, maxOutput: 131072},
	"qwen3.8-2.4t-a95b":            {contextWindow: 1048576, maxInput: 1048576, maxOutput: 131072},
	"qwen3.8-27b":                  {contextWindow: 1024000, maxInput: 1024000, maxOutput: 235929},
	"qwen3.7-max":                  {contextWindow: 1000000, maxInput: 1000000, maxOutput: 131072},
	"qwen3.7-plus":                 {contextWindow: 1000000, maxInput: 1000000, maxOutput: 131072},
	"qwen3.7-flash":                {contextWindow: 1000000, maxInput: 991808, maxOutput: 131072},
	"qwen3.6-max-preview":          {contextWindow: 262144, maxInput: 262144, maxOutput: 65536},
	"qwen3.6-plus":                 {contextWindow: 1000000, maxInput: 1000000, maxOutput: 65536},
	"qwen3.6-flash":                {contextWindow: 1000000, maxInput: 1000000, maxOutput: 65536},
	"qwen3.6-35b-a3b":              {contextWindow: 262144, maxInput: 262144, maxOutput: 65536},
	"qwen3.6-27b":                  {contextWindow: 262144, maxInput: 262144, maxOutput: 65536},
	"qwen3.5-plus":                 {contextWindow: 1000000, maxInput: 1000000, maxOutput: 65536},
	"qwen3.5-flash":                {contextWindow: 1000000, maxInput: 1000000, maxOutput: 65536},
	"qwen3.5-397b-a17b":            {contextWindow: 262144, maxInput: 262144, maxOutput: 65536},
	"qwen3.5-122b-a10b":            {contextWindow: 262144, maxInput: 262144, maxOutput: 65536},
	"qwen3.5-35b-a3b":              {contextWindow: 262144, maxInput: 262144, maxOutput: 65536},
	"qwen3.5-27b":                  {contextWindow: 262144, maxInput: 262144, maxOutput: 65536},
	"qwen3-max":                    {contextWindow: 262144, maxInput: 262144, maxOutput: 65536},
	"qwen3-coder-plus":             {contextWindow: 1048576, maxInput: 1048576, maxOutput: 65536},
	"qwen3-coder-flash":            {contextWindow: 1000000, maxInput: 1000000, maxOutput: 65536},
	"qwen3-coder":                  {contextWindow: 262144, maxInput: 262144, maxOutput: 65536},
	"qwen3-coder-next":             {contextWindow: 262144, maxInput: 262144, maxOutput: 65536},
	"qwen3-coder-480b-a35b-instruct": {contextWindow: 262144, maxInput: 262144, maxOutput: 65536},
	"qwen3-coder-30b-a3b-instruct": {contextWindow: 262144, maxInput: 262144, maxOutput: 65536},
	"qwen-plus":                    {contextWindow: 1000000, maxInput: 1000000, maxOutput: 32768},
	"qwen-flash":                   {contextWindow: 1000000, maxInput: 1000000, maxOutput: 32768},
	"qwen-turbo":                   {contextWindow: 1000000, maxInput: 1000000, maxOutput: 16384},
	"qwen-max":                     {contextWindow: 131072, maxInput: 129024, maxOutput: 8192},
	"qwen-coder-plus":              {contextWindow: 131072, maxInput: 129024, maxOutput: 8192},
	"qwen2.5-coder-32b-instruct":   {contextWindow: 131072, maxInput: 131072, maxOutput: 8192},

	// Moonshot Kimi K3.1 / K3 / K2.7 / K2.6 / K2.5 / K2
	"kimi-k3.1-pro":                {contextWindow: 1048576, maxInput: 1048576, maxOutput: 1048576},
	"kimi-k3.1-pro-highspeed":      {contextWindow: 1048576, maxInput: 1048576, maxOutput: 1048576},
	"kimi-k3":                      {contextWindow: 1048576, maxInput: 1048576, maxOutput: 1048576},
	"kimi-k2.7-code":               {contextWindow: 262144, maxInput: 262144, maxOutput: 262144},
	"kimi-k2.7-code-highspeed":     {contextWindow: 262144, maxInput: 262144, maxOutput: 262144},
	"kimi-k2.6":                    {contextWindow: 262144, maxInput: 262144, maxOutput: 262144},
	"kimi-k2.5":                    {contextWindow: 262144, maxInput: 262144, maxOutput: 65536},
	"kimi-k2-thinking":             {contextWindow: 262144, maxInput: 262144, maxOutput: 98304},
	"kimi-k2-0905":                 {contextWindow: 262144, maxInput: 262144, maxOutput: 98304},
	"kimi-k2":                      {contextWindow: 131072, maxInput: 131072, maxOutput: 98304},

	// Zhipu GLM 5.3 / 5.2 / 5.1 / 5 / 4.7 / 4.6 / 4.5
	"glm-5.3":        {contextWindow: 1000000, maxInput: 1000000, maxOutput: 131072},
	"glm-5.3-prime":  {contextWindow: 1000000, maxInput: 1000000, maxOutput: 131072},
	"glm-5.3-flash":  {contextWindow: 1000000, maxInput: 1000000, maxOutput: 131072},
	"glm-5.3-flashx": {contextWindow: 1000000, maxInput: 1000000, maxOutput: 131072},
	"glm-5.2":        {contextWindow: 1000000, maxInput: 1000000, maxOutput: 131072},
	"glm-5.1":        {contextWindow: 200000, maxInput: 200000, maxOutput: 131072},
	"glm-5":          {contextWindow: 204800, maxInput: 204800, maxOutput: 131072},
	"glm-5-turbo":    {contextWindow: 202752, maxInput: 202752, maxOutput: 131072},
	"glm-5v-turbo":   {contextWindow: 200000, maxInput: 200000, maxOutput: 131072},
	"glm-4.7":        {contextWindow: 204800, maxInput: 204800, maxOutput: 131072},
	"glm-4.7-flash":  {contextWindow: 200000, maxInput: 200000, maxOutput: 131072},
	"glm-4.7-flashx": {contextWindow: 200000, maxInput: 200000, maxOutput: 131072},
	"glm-4.6":        {contextWindow: 204800, maxInput: 204800, maxOutput: 131072},
	"glm-4.6v":       {contextWindow: 128000, maxInput: 128000, maxOutput: 32768},
	"glm-4.6v-flash": {contextWindow: 128000, maxInput: 128000, maxOutput: 32768},
	"glm-4.5":        {contextWindow: 131072, maxInput: 131072, maxOutput: 98304},
	"glm-4.5-air":    {contextWindow: 131072, maxInput: 131072, maxOutput: 98304},
	"glm-4.5-flash":  {contextWindow: 131072, maxInput: 131072, maxOutput: 98304},

	// MiniMax M3.1 / M3 / M2.7 / M2.5 / M2.1 / M2 / M1
	"minimax-m3.1-flash":     {contextWindow: 1000000, maxInput: 1000000, maxOutput: 524288},
	"minimax-m3":             {contextWindow: 1000000, maxInput: 1000000, maxOutput: 524288},
	"minimax-m2.7":           {contextWindow: 204800, maxInput: 204800, maxOutput: 131072},
	"minimax-m2.7-highspeed": {contextWindow: 204800, maxInput: 204800, maxOutput: 131072},
	"minimax-m2.5":           {contextWindow: 204800, maxInput: 204800, maxOutput: 131072},
	"minimax-m2.5-highspeed": {contextWindow: 204800, maxInput: 204800, maxOutput: 131072},
	"minimax-m2.1":           {contextWindow: 204800, maxInput: 204800, maxOutput: 131072},
	"minimax-m2":             {contextWindow: 204800, maxInput: 204800, maxOutput: 131072},
	"minimax-m1":             {contextWindow: 1000000, maxInput: 1000000, maxOutput: 40000},
}

// knownProviderNamespaces 限定仅剥离已知官方/聚合命名空间前缀，不对任意未知路径做模糊拆解。
var knownProviderNamespaces = map[string]struct{}{
	"openai":     {},
	"anthropic":  {},
	"google":     {},
	"deepseek":   {},
	"x-ai":       {},
	"xai":        {},
	"qwen":       {},
	"alibaba":    {},
	"moonshotai": {},
	"moonshot":   {},
	"zhipuai":    {},
	"z-ai":       {},
	"zai":        {},
	"minimax":    {},
	"models":     {},
}

func lookupExactRegistryID(raw string) (registryEntry, bool) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return registryEntry{}, false
	}
	if entry, ok := curatedModelRegistry[normalized]; ok {
		return entry, true
	}
	if slash := strings.IndexByte(normalized, '/'); slash > 0 && slash < len(normalized)-1 {
		prefix := normalized[:slash]
		rest := normalized[slash+1:]
		if _, knownPrefix := knownProviderNamespaces[prefix]; knownPrefix {
			if entry, ok := curatedModelRegistry[rest]; ok {
				return entry, true
			}
		}
	}
	return registryEntry{}, false
}

// LookupRegistryModelLimits 在内置注册表中精确查找模型或底层模型容量。
// 若 modelID 为未识别的自定义别名（如 gemini-3.8-flash-high），仅当 baseModelID 精确命中时才返回注册表容量。
func LookupRegistryModelLimits(modelID, baseModelID string) (ModelLimits, bool) {
	if entry, ok := lookupExactRegistryID(modelID); ok {
		return ModelLimits{
			ContextWindowTokens: entry.contextWindow,
			MaxInputTokens:      entry.maxInput,
			MaxOutputTokens:     entry.maxOutput,
			Source:              LimitsSourceRegistry,
		}, true
	}
	if entry, ok := lookupExactRegistryID(baseModelID); ok {
		return ModelLimits{
			ContextWindowTokens: entry.contextWindow,
			MaxInputTokens:      entry.maxInput,
			MaxOutputTokens:     entry.maxOutput,
			Source:              LimitsSourceRegistry,
		}, true
	}
	return ModelLimits{Source: LimitsSourceUnknown}, false
}

// ResolveModelLimits 按优先级解析模型上下文限制：
// 1. 用户手动覆盖 (user)
// 2. 服务商返回的模型信息 (provider)
// 3. models.dev 等注册表精确匹配 (registry，支持 model 或用户显式指定的 base_model)
// 4. 未知 (unknown，保留 0 值，不伪造默认容量)
func ResolveModelLimits(
	userOverride ModelLimits,
	providerLimits *ModelLimits,
	modelID string,
	baseModelID string,
	now time.Time,
) ModelLimits {
	verifiedAt := now.UTC()
	if verifiedAt.IsZero() {
		verifiedAt = time.Now().UTC()
	}

	// 优先级 1：用户手动配置
	if userOverride.Source == LimitsSourceUser && userOverride.HasKnownCapacity() {
		out := ModelLimits{
			ContextWindowTokens: sanitizePositiveTokenLimit(userOverride.ContextWindowTokens),
			MaxInputTokens:      sanitizePositiveTokenLimit(userOverride.MaxInputTokens),
			MaxOutputTokens:     sanitizePositiveTokenLimit(userOverride.MaxOutputTokens),
			Source:              LimitsSourceUser,
			VerifiedAt:          userOverride.VerifiedAt,
		}
		if out.VerifiedAt == nil {
			out.VerifiedAt = &verifiedAt
		}
		out = clampInputOutputToContextWindow(out)
		if out.HasKnownCapacity() {
			return out
		}
	}

	// 优先级 2：服务商返回的模型信息
	if providerLimits != nil {
		candidate := clampInputOutputToContextWindow(ModelLimits{
			ContextWindowTokens: sanitizePositiveTokenLimit(providerLimits.ContextWindowTokens),
			MaxInputTokens:      sanitizePositiveTokenLimit(providerLimits.MaxInputTokens),
			MaxOutputTokens:     sanitizePositiveTokenLimit(providerLimits.MaxOutputTokens),
			Source:              LimitsSourceProvider,
			VerifiedAt:          providerLimits.VerifiedAt,
		})
		if candidate.ContextWindowTokens > 0 || candidate.MaxInputTokens > 0 {
			if candidate.VerifiedAt == nil {
				candidate.VerifiedAt = &verifiedAt
			}
			return candidate
		}
	}

	// 优先级 3：models.dev 等注册表（仅对精确识别的 model 或 base_model 生效）
	if reg, ok := LookupRegistryModelLimits(modelID, baseModelID); ok {
		reg.VerifiedAt = &verifiedAt
		return reg
	}

	// 优先级 4：未知（不伪造容量）
	return ModelLimits{
		Source: LimitsSourceUnknown,
	}
}

func sanitizePositiveTokenLimit(v int) int {
	if v <= 0 || v > maxReasonableTokenLimit {
		return 0
	}
	return v
}

func clampInputOutputToContextWindow(limits ModelLimits) ModelLimits {
	if limits.ContextWindowTokens > 0 {
		if limits.MaxInputTokens > limits.ContextWindowTokens {
			limits.MaxInputTokens = limits.ContextWindowTokens
		}
		if limits.MaxOutputTokens > limits.ContextWindowTokens {
			limits.MaxOutputTokens = limits.ContextWindowTokens
		}
	}
	return limits
}
