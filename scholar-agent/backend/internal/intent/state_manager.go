package intent

import (
	"fmt"
	"log"
	"time"
)

// StateManager 模式状态管理器 - 管理用户当前所处的科研模式
type StateManager struct {
	cache *MemoryCache
}

// NewStateManager 创建模式状态管理器
func NewStateManager(cache *MemoryCache) *StateManager {
	return &StateManager{cache: cache}
}

// GetCurrentMode 获取用户当前意图模式
func (sm *StateManager) GetCurrentMode(userId int64, deviceId string) (CommandType, bool) {
	key := IntentTypeKey(userId, deviceId)
	val, ok := sm.cache.GetString(key)
	if !ok {
		return CommandNone, false
	}
	ct := CommandTypeFromString(val)
	if ct == CommandNone {
		return CommandNone, false
	}
	log.Printf("[StateManager] 用户 %d 当前模式: %s", userId, val)
	return ct, true
}

// SetMode 设置用户意图模式
func (sm *StateManager) SetMode(userId int64, deviceId string, mode CommandType, ttl time.Duration) {
	if mode == CommandNone {
		return
	}
	key := IntentTypeKey(userId, deviceId)
	sm.cache.Set(key, string(mode), ttl)
	log.Printf("[StateManager] 设置用户 %d 模式: %s (TTL=%v)", userId, mode, ttl)
}

// ClearMode 清除用户意图模式
func (sm *StateManager) ClearMode(userId int64, deviceId string) {
	key := IntentTypeKey(userId, deviceId)
	sm.cache.Delete(key)
	log.Printf("[StateManager] 清除用户 %d 模式", userId)
}

// RefreshMode 刷新模式过期时间（用户在模式内继续操作时延长）
func (sm *StateManager) RefreshMode(userId int64, deviceId string, mode CommandType, ttl time.Duration) {
	key := IntentTypeKey(userId, deviceId)
	sm.cache.Set(key, string(mode), ttl)
}

// SetLastHistory 保存上一轮对话记录
func (sm *StateManager) SetLastHistory(userId int64, deviceId string, history string) {
	key := LastHistoryKey(userId, deviceId)
	sm.cache.Set(key, history, CacheKeyLastHistoryTTL)
}

// GetLastHistory 获取上一轮对话记录
func (sm *StateManager) GetLastHistory(userId int64, deviceId string) string {
	key := LastHistoryKey(userId, deviceId)
	val, ok := sm.cache.GetString(key)
	if !ok {
		return ""
	}
	return val
}

// HandleModeCheck 检查用户是否在特定模式中，返回是否需要模式内处理
// 如果向量匹配到 EXIT 类型，则退出当前模式
func (sm *StateManager) HandleModeCheck(currentMode CommandType, vectorInfos []IntentVectorInfo) (shouldContinueInMode bool, shouldExit bool) {
	if currentMode == CommandNone {
		return false, false
	}

	// 检查是否有退出意图
	for _, vi := range vectorInfos {
		if vi.Type == VectorTypeExit && vi.ActionType == currentMode {
			return false, true // 需要退出模式
		}
	}

	// 在模式中继续处理
	return true, false
}

// GetModeDescription 获取模式的中文描述
func GetModeDescription(mode CommandType) string {
	descriptions := map[CommandType]string{
		CommandPaperReading:     "论文阅读模式",
		CommandWritingAssist:    "写作辅助模式",
		CommandDataAnalysis:     "数据分析模式",
		CommandLiteratureReview: "文献综述模式",
		CommandExperimentDesign: "实验设计模式",
		CommandFormulaDerive:    "公式推导模式",
	}
	if desc, ok := descriptions[mode]; ok {
		return desc
	}
	return fmt.Sprintf("未知模式(%s)", string(mode))
}
