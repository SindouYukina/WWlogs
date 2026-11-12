// Code generated from the Global 3.8.9 client's Protocol.js (SHA-256 3356189638b3c531a2ff3b249dc2b07f1fbc0943f4b2ca9674d5bcb0790eed31) and NetDefine.js
// (SHA-256 26f276ef8563de7b1e46a8893dc8546f82e6a78422fa42cd983cfd983c4492ef). DO NOT EDIT; regenerate it from the next client version instead.

package capture

// Fields of Global 3.8.9, by message; each comment is the client's minified message and property name.
const (
	// BasicInfoNotify (hYn)
	fieldBasicInfoNotifyBirthday = 7 // hYn.ZVn

	// CheckClientPush (Roa)
	fieldCheckClientPushAppVersion      = 1 // Roa.Y9n
	fieldCheckClientPushLauncherVersion = 2 // Roa.J9n
	fieldCheckClientPushResourceVersion = 3 // Roa.z9n

	// ClientBasicInfo (Z9n)
	fieldClientBasicInfoDeviceID     = 2  // Z9n.oHn
	fieldClientBasicInfoScreenHeight = 15 // Z9n.hjf
	fieldClientBasicInfoScreenWidth  = 16 // Z9n.ljf
	fieldClientBasicInfoOSVersion    = 20 // Z9n.iyg

	// ClientCurrentRoleReportPush (Xe_)
	fieldClientCurrentRoleReportPushCurrentRoleID = 4 // Xe_.YHn

	// CombatPushData (CombatMessage.OFs)
	fieldCombatPushDataEndSkillPush = 12 // CombatMessage.OFs.ne_

	// DamageExecuteResponse (lis)
	fieldDamageExecuteResponseIsCrit = 3 // lis.sAs

	// DependenceComponentPb (iI_)
	fieldDependenceComponentPbDependenceEntityIDs = 1 // iI_.MI_

	// EndSkillPush (ne_)
	fieldEndSkillPushUseSkillInfo = 10 // ne_.dVn

	// EntityAddNotify (res)
	fieldEntityAddNotifyIsAdd = 9 // res.lWn

	// EntityComponentPb (m3s)
	fieldEntityComponentPbDependenceComponentPb = 50 // m3s.iI_

	// FightBuffEffectContext (u3s)
	fieldFightBuffEffectContextLeftCooldown = 1 // u3s.BTs

	// LoginRequest (fss)
	fieldLoginRequestDevLoginCheckData         = 9  // fss.e7n
	fieldLoginRequestPublicResourceVersionInfo = 10 // fss.c7n

	// PlayerSceneGroupData (ERs)
	fieldPlayerSceneGroupDataGroup   = 1 // ERs.USs
	fieldPlayerSceneGroupDataCurRole = 3 // ERs.NVn

	// PlayerSceneGroupDataList (yRs)
	fieldPlayerSceneGroupDataListCurrentGroup = 3 // yRs.ZI_

	// SceneInformation (YFs)
	fieldSceneInformationSceneID = 1 // YFs.BKn

	// SceneRoleInformation (KFs)
	fieldSceneRoleInformationOnStageOutOfControl = 3 // KFs.eT_

	// UseSkillInformation (W3s)
	fieldUseSkillInformationSkillID   = 3 // W3s.r5n
	fieldUseSkillInformationTimeStamp = 7 // W3s.J8n
)
