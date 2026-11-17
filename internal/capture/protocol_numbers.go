// Code generated from the Global 3.8.9 client's Protocol.js (SHA-256 3356189638b3c531a2ff3b249dc2b07f1fbc0943f4b2ca9674d5bcb0790eed31) and NetDefine.js
// (SHA-256 26f276ef8563de7b1e46a8893dc8546f82e6a78422fa42cd983cfd983c4492ef). DO NOT EDIT; regenerate it from the next client version instead.

package capture

// currentGameVersion is the major.minor game version these numbers belong to. A protocol session is decoded only
// after it reports this version (protocol_session.go); the numbers and this version change together.
const currentGameVersion = "3.8"

// currentProtocolClient names the client whose Protocol.js and NetDefine.js these numbers come from.
const currentProtocolClient = "Global 3.8.9"

// Commands of Global 3.8.9; each comment is the client's minified message name.
const (
	CmdLoginRequest                            = 103   // fss
	CmdProtoKeyResponse                        = 112   // Lss
	CmdCheckClientPush                         = 116   // Roa
	CmdAtomicProcessAddBuffResponse            = 15504 // Rxf
	CmdRemoveBuffByServerIDS2cRequestNotify    = 15505 // rpm
	CmdRemoveBuffByIDS2cResponsePush           = 15939 // N4n
	CmdOrderApplyBuffResponse                  = 15979 // uzn
	CmdRemoveGameplayEffectRequest             = 15998 // x3n
	CmdActivateBuffPush                        = 16028 // pe_
	CmdRemoveBuffS2cResponsePush               = 16300 // O4n
	CmdBuffStackCountResponse                  = 16438 // fzn
	CmdSimpleCombatEntityAttributeUpdateNotify = 16636 // ecC
	cmdTimeStopPush                            = 16707 // GCs
	CmdUpdatePlayersFightRolesInfoNotify       = 16745 // Ous
	CmdApplyBuffS2cResponsePush                = 17435 // G4n
	CmdApplyGameplayEffectPush                 = 17445 // ie_
	CmdApplyBuffS2cRequestNotify               = 17614 // d3n
	CmdOrderRemoveBuffResponse                 = 17733 // czn
	CmdBuffStackCountPush                      = 17747 // Re_
	CmdEntityRemoveNotify                      = 17927 // oes
	CmdRemoveGameplayEffectNotify              = 18172 // IFn
	CmdSimpleCombatEntityBuffUpdateNotify      = 18273 // Zju
	CmdEntityRemoveDynamicInteractNotify       = 18312 // Bes
	CmdBuffEffectExecutePush                   = 18599 // Vzd
	CmdDamageExecuteRequest                    = 18654 // U3n
	CmdMovePackagePush                         = 18855 // Yus
	CmdEntityBuffProducerRequest               = 18892 // _es
	CmdEntityBuffProducerResponse              = 18914 // ues
	CmdActivateBuffResponse                    = 19166 // dzn
	CmdAddSysBuffNotify                        = 19225 // Hcs
	CmdAtomicProcessRemoveBuffRequest          = 19269 // hMg
	CmdBuffStackCountNotify                    = 19317 // i3n
	CmdBuffEffectExecuteRequest                = 19635 // Czn
	CmdEntityActiveResponse                    = 19734 // fes
	CmdOrderApplyBuffNotify                    = 19792 // HFn
	CmdEntityCommonTagNotify                   = 20031 // Lcs
	CmdBuffEffectResponse                      = 20053 // Ezn
	CmdOrderRemoveBuffByTagsRequest            = 20210 // Y3n
	CmdEntityAddNotify                         = 20302 // res
	CmdOrderRemoveBuffByTagsResponse           = 20335 // mzn
	CmdJoinSceneNotify                         = 20597 // Kus
	CmdUpdatePlayerAllFightRoleNotify          = 20791 // Gus
	CmdOrderRemoveBuffNotify                   = 20935 // jFn
	CmdOrderApplyBuffRequest                   = 21297 // Q3n
	CmdCombatSendPackResponse                  = 21536 // CombatMessage.aZn
	CmdTransformBuffStackNotify                = 21805 // x9f
	CmdBasicInfoNotify                         = 21993 // hYn
	CmdUpdateSceneGroupDataNotify              = 22332 // Hgs
	CmdBuffEffectRequest                       = 22402 // I4n
	CmdEntityBuffProducerOperateResponse       = 22978 // des
	CmdRemoveSysBuffNotify                     = 23366 // Wcs
	CmdOrderRemoveBuffByTagsNotify             = 23871 // KFn
	CmdApplyGameplayEffectResponse             = 23904 // lzn
	CmdUpdateSysBuffNotify                     = 23927 // jcs
	CmdBuffStackCountRequest                   = 24150 // u4n
	CmdSimpleCombatEntitySyncAttributePush     = 24434 // pGd
	CmdBuffDurationNotify                      = 24676 // W7l
	CmdAtomicProcessRemoveBuffResponse         = 24883 // lMg
	CmdRefreshBuffDurationPush                 = 24982 // GG1
	CmdEntityBuffProducerOperateRequest        = 25261 // ces
	CmdRemoveBuffByIDS2cRequestNotify          = 25284 // M3n
	CmdChangePlayerFightRoleNotify             = 25527 // Nus
	CmdApplyGameplayEffectNotify               = 26040 // yFn
	CmdEntityActiveRequest                     = 26157 // ges
	CmdPlayerLivingStatesNotify                = 26412 // w7s
	CmdCombatSendPackRequest                   = 26497 // CombatMessage.sZn
	CmdBuffEffectPush                          = 26677 // Ge_
	CmdOrderRemoveBuffRequest                  = 26903 // X3n
	CmdApplyGameplayEffectRequest              = 26992 // R3n
	CmdEntityStateReadyNotify                  = 27022 // Les
	CmdPlayerFollowerNotify                    = 27250 // Summon.eJs
	CmdActivateBuffRequest                     = 27416 // $3n
	CmdAtomicProcessAddBuffRequest             = 27678 // wxf
	cmdUpdateTimerInfoNotify                   = 27863 // AJn
	CmdBuffEffectExecuteResponse               = 27923 // gzn
	cmdTimeCheckResponse                       = 28031 // QCs
	CmdRemoveBuffS2cRequestNotify              = 28392 // C3n
	cmdTimeCheckRequest                        = 28799 // WCs
	CmdUpdatePlayerSingleFightRoleNotify       = 28925 // kus
	CmdSimpleCombatEntityBuffLayerCountNotify  = 29187 // J0d
	CmdRemoveGameplayEffectPush                = 29542 // re_
	CmdPhantomAttrRecommendResponse            = 29590 // Yv_
	CmdActivateBuffNotify                      = 29719 // WFn
	CmdRemoveGameplayEffectResponse            = 29948 // _zn
	CmdCombatReceivePackNotify                 = 29973 // CombatMessage.hZn
)

// Fields of Global 3.8.9, by message; each comment is the client's minified message and property name.
const (
	// ActivateBuffNotify (WFn)
	FieldActivateBuffNotifyOn     = 2 // WFn.qjn
	FieldActivateBuffNotifyHandle = 7 // WFn.uVn

	// ApplyBuffS2cRequestNotify (d3n)
	FieldApplyBuffS2cRequestNotifyInstigatorID = 3  // d3n.Rjn
	fieldApplyBuffS2cRequestNotifyReason       = 4  // d3n.x9n
	FieldApplyBuffS2cRequestNotifyID           = 5  // d3n.s5n
	fieldApplyBuffS2cRequestNotifyIsIterable   = 6  // d3n.Pjn
	fieldApplyBuffS2cRequestNotifyLevel        = 7  // d3n.F6n
	fieldApplyBuffS2cRequestNotifyDuration     = 9  // d3n.n5n
	fieldApplyBuffS2cRequestNotifyServerID     = 12 // d3n.wjn
	fieldApplyBuffS2cRequestNotifyStackCount   = 13 // d3n.Bjn
	fieldApplyBuffS2cRequestNotifyApplyType    = 15 // d3n.xjn

	// ApplyBuffS2cResponsePush (G4n)
	fieldApplyBuffS2cResponsePushIsActive  = 8  // G4n.WHn
	fieldApplyBuffS2cResponsePushHandle    = 10 // G4n.uVn
	fieldApplyBuffS2cResponsePushErrorCode = 13 // G4n.Q4n

	// ApplyGameplayEffectNotify (yFn)
	FieldApplyGameplayEffectNotifyDuration     = 1  // yFn.n5n
	fieldApplyGameplayEffectNotifyServerID     = 2  // yFn.wjn
	FieldApplyGameplayEffectNotifyLeftDuration = 3  // yFn.QEs
	fieldApplyGameplayEffectNotifyApplyType    = 4  // yFn.xjn
	FieldApplyGameplayEffectNotifyHandle       = 6  // yFn.uVn
	fieldApplyGameplayEffectNotifyIsActive     = 7  // yFn.WHn
	FieldApplyGameplayEffectNotifyInstigatorID = 9  // yFn.Rjn
	FieldApplyGameplayEffectNotifyStackCount   = 10 // yFn.Bjn
	fieldApplyGameplayEffectNotifyLevel        = 11 // yFn.F6n
	FieldApplyGameplayEffectNotifyConfBuffID   = 12 // yFn.tcp
	FieldApplyGameplayEffectNotifyID           = 13 // yFn.s5n
	FieldApplyGameplayEffectNotifyEntityID     = 14 // yFn.F4n

	// ApplyGameplayEffectPush (ie_)
	FieldApplyGameplayEffectPushReason       = 1  // ie_.x9n
	fieldApplyGameplayEffectPushServerID     = 3  // ie_.wjn
	fieldApplyGameplayEffectPushIsActive     = 4  // ie_.WHn
	fieldApplyGameplayEffectPushApplyType    = 6  // ie_.xjn
	fieldApplyGameplayEffectPushLevel        = 7  // ie_.F6n
	FieldApplyGameplayEffectPushHandle       = 8  // ie_.uVn
	FieldApplyGameplayEffectPushInstigatorID = 10 // ie_.Rjn
	FieldApplyGameplayEffectPushDuration     = 11 // ie_.n5n
	FieldApplyGameplayEffectPushStackCount   = 12 // ie_.Bjn
	fieldApplyGameplayEffectPushConfBuffID   = 14 // ie_.tcp
	FieldApplyGameplayEffectPushID           = 15 // ie_.s5n

	// AttributeChangedNotify (OFn)
	FieldAttributeChangedNotifyAttributes = 6 // OFn.GSs

	// BasicInfoNotify (hYn)
	FieldBasicInfoNotifyID = 4 // hYn.s5n

	// BuffDurationNotify (W7l)
	FieldBuffDurationNotifyLeftDuration = 6  // W7l.QEs
	FieldBuffDurationNotifyHandleID     = 8  // W7l.cVn
	FieldBuffDurationNotifyDuration     = 13 // W7l.n5n

	// BuffStackCountNotify (i3n)
	FieldBuffStackCountNotifyLeftDuration       = 2  // i3n.QEs
	FieldBuffStackCountNotifyHandleID           = 5  // i3n.cVn
	FieldBuffStackCountNotifyNewStackCount      = 6  // i3n.Gjn
	fieldBuffStackCountNotifyNotRefreshDuration = 7  // i3n.$L1
	fieldBuffStackCountNotifyInstigatorID       = 10 // i3n.Rjn
	FieldBuffStackCountNotifyDuration           = 13 // i3n.n5n
	fieldBuffStackCountNotifyNotRefreshPeriod   = 15 // i3n.WL1

	// BuffStackCountPush (Re_)
	fieldBuffStackCountPushNotRefreshPeriod   = 1  // Re_.WL1
	fieldBuffStackCountPushIsPrematureRemoval = 2  // Re_.Ojn
	FieldBuffStackCountPushNotRefreshDuration = 3  // Re_.$L1
	FieldBuffStackCountPushDuration           = 5  // Re_.n5n
	fieldBuffStackCountPushReason             = 7  // Re_.x9n
	FieldBuffStackCountPushNewStackCount      = 8  // Re_.Gjn
	FieldBuffStackCountPushHandleID           = 9  // Re_.cVn
	FieldBuffStackCountPushInstigatorID       = 10 // Re_.Rjn

	// ChangeStateNotify (e3n)
	FieldChangeStateNotifyToState   = 4  // e3n.z4n
	FieldChangeStateNotifyFromState = 7  // e3n.J4n
	FieldChangeStateNotifyFsmID     = 12 // e3n.$4n

	// ClientBasicInfo (Z9n)
	fieldClientBasicInfoClientVersion = 19 // Z9n.cjf

	// ClientCurrentRoleReportPush (Xe_)
	FieldClientCurrentRoleReportPushCurrentEntityID = 10 // Xe_.JHn

	// CombatCommon (K8n)
	fieldCombatCommonPreMessageID = 1 // K8n.X8n
	FieldCombatCommonMessageID    = 2 // K8n.$8n
	fieldCombatCommonOriginator   = 3 // K8n.Y8n
	fieldCombatCommonTimeStamp    = 4 // K8n.J8n
	FieldCombatCommonEntityID     = 5 // K8n.F4n

	// CombatNotifyData (CombatMessage.KLs)
	FieldCombatNotifyDataCombatCommon                   = 1  // CombatMessage.KLs.K8n
	FieldCombatNotifyDataDamageExecuteNotify            = 4  // CombatMessage.KLs.EFn
	FieldCombatNotifyDataApplyGameplayEffectNotify      = 5  // CombatMessage.KLs.yFn
	FieldCombatNotifyDataRemoveGameplayEffectNotify     = 6  // CombatMessage.KLs.IFn
	FieldCombatNotifyDataAttributeChangedNotify         = 22 // CombatMessage.KLs.OFn
	FieldCombatNotifyDataActivateBuffNotify             = 29 // CombatMessage.KLs.WFn
	FieldCombatNotifyDataChangeStateNotify              = 38 // CombatMessage.KLs.e3n
	FieldCombatNotifyDataBuffStackCountNotify           = 41 // CombatMessage.KLs.i3n
	FieldCombatNotifyDataApplyBuffS2CRequestNotify      = 53 // CombatMessage.KLs.d3n
	fieldCombatNotifyDataRemoveBuffS2CRequestNotify     = 54 // CombatMessage.KLs.C3n
	FieldCombatNotifyDataRecoverPropChangedNotify       = 58 // CombatMessage.KLs.v3n
	FieldCombatNotifyDataRemoveBuffByIDS2CRequestNotify = 59 // CombatMessage.KLs.M3n
	fieldCombatNotifyDataPlayerBattleStateChangeNotify  = 62 // CombatMessage.KLs.y3n
	FieldCombatNotifyDataBuffDurationNotify             = 73 // CombatMessage.KLs.W7l

	// CombatPushData (CombatMessage.OFs)
	FieldCombatPushDataCombatCommon                  = 1  // CombatMessage.OFs.K8n
	fieldCombatPushDataApplyBuffS2CResponsePush      = 4  // CombatMessage.OFs.G4n
	fieldCombatPushDataRemoveBuffByIDS2CResponsePush = 6  // CombatMessage.OFs.N4n
	FieldCombatPushDataApplyGameplayEffectPush       = 9  // CombatMessage.OFs.ie_
	FieldCombatPushDataRemoveGameplayEffectPush      = 10 // CombatMessage.OFs.re_
	FieldCombatPushDataBuffStackCountPush            = 37 // CombatMessage.OFs.Re_
	fieldCombatPushDataRTimeStopPush                 = 47 // CombatMessage.OFs.Fe_
	FieldCombatPushDataClientCurrentRoleReportPush   = 56 // CombatMessage.OFs.Xe_

	// CombatReceiveData (CombatMessage.$Fs)
	FieldCombatReceiveDataCombatNotifyData   = 2 // CombatMessage.$Fs.KLs
	FieldCombatReceiveDataCombatResponseData = 3 // CombatMessage.$Fs.QLs

	// CombatReceivePackNotify (CombatMessage.hZn)
	FieldCombatReceivePackNotifyData = 4 // CombatMessage.hZn.R5n

	// CombatRequestData (CombatMessage.FFs)
	FieldCombatRequestDataRequestID            = 2 // CombatMessage.FFs.W8n
	FieldCombatRequestDataDamageExecuteRequest = 5 // CombatMessage.FFs.U3n

	// CombatResponseData (CombatMessage.QLs)
	FieldCombatResponseDataRequestID             = 2 // CombatMessage.QLs.W8n
	FieldCombatResponseDataDamageExecuteResponse = 5 // CombatMessage.QLs.lis

	// CombatSendData (CombatMessage.VFs)
	FieldCombatSendDataPush    = 2 // CombatMessage.VFs.Q8n
	FieldCombatSendDataRequest = 3 // CombatMessage.VFs.x5n

	// CombatSendPackRequest (CombatMessage.sZn)
	FieldCombatSendPackRequestData = 11 // CombatMessage.sZn.R5n

	// CombatSendPackResponse (CombatMessage.aZn)
	FieldCombatSendPackResponseReceivePackNotify = 1 // CombatMessage.aZn.XLs

	// ConcomitantsComponentPb (Dys)
	fieldConcomitantsComponentPbVisionEntityIDs = 1 // Dys.wzm
	fieldConcomitantsComponentPbCustomEntityIDs = 2 // Dys.OIs
	fieldConcomitantsComponentPbPhantomRoleEid  = 3 // Dys.kIs
	fieldConcomitantsComponentPbWeakVisionEid   = 4 // Dys.Vnm

	// DamageContext (Njn)
	FieldDamageContextSourceType     = 1 // Njn.Vjn
	FieldDamageContextBulletID       = 2 // Njn.Mjn
	FieldDamageContextBulletTags     = 3 // Njn.Hjn
	FieldDamageContextSkillID        = 4 // Njn.r5n
	FieldDamageContextSkillMessageID = 5 // Njn.ptc

	// DamageExecuteNotify (EFn)
	FieldDamageExecuteNotifyChangeLife        = 1  // EFn.jQ_
	FieldDamageExecuteNotifyTargetEntityID    = 2  // EFn.TVn
	FieldDamageExecuteNotifyIsCrit            = 3  // EFn.sAs
	FieldDamageExecuteNotifyElementType       = 4  // EFn.wHn
	FieldDamageExecuteNotifyDamageContext     = 5  // EFn.Njn
	FieldDamageExecuteNotifyShieldCoverDamage = 6  // EFn.hAs
	FieldDamageExecuteNotifyDamage            = 7  // EFn.nAs
	FieldDamageExecuteNotifyKillerEntityID    = 8  // EFn.GUp
	FieldDamageExecuteNotifyChangeWeakness    = 9  // EFn.ekf
	FieldDamageExecuteNotifyAttackerEntityID  = 10 // EFn.kjn
	FieldDamageExecuteNotifyPartID            = 11 // EFn.jjn
	FieldDamageExecuteNotifyDamageID          = 12 // EFn.Fjn
	FieldDamageExecuteNotifySkillLevel        = 13 // EFn.Wjn
	FieldDamageExecuteNotifyKilledTarget      = 14 // EFn.aAs
	FieldDamageExecuteNotifyImmuneType        = 15 // EFn.lAs

	// DamageExecuteRequest (U3n)
	FieldDamageExecuteRequestTargetEntityID   = 1  // U3n.TVn
	fieldDamageExecuteRequestDamageContext    = 2  // U3n.Njn
	FieldDamageExecuteRequestAttackerEntityID = 5  // U3n.kjn
	fieldDamageExecuteRequestSkillLevel       = 7  // U3n.Wjn
	FieldDamageExecuteRequestPartID           = 10 // U3n.jjn
	FieldDamageExecuteRequestDamageID         = 14 // U3n.Fjn

	// DamageExecuteResponse (lis)
	FieldDamageExecuteResponseErrorCode  = 5 // lis.Q4n
	FieldDamageExecuteResponseChangeLife = 6 // lis.jQ_
	FieldDamageExecuteResponseImmuneType = 7 // lis.lAs
	FieldDamageExecuteResponseDamage     = 9 // lis.nAs

	// DynamicEntityInformation (HFs)
	fieldDynamicEntityInformationID           = 1  // HFs.s5n
	fieldDynamicEntityInformationEntityType   = 2  // HFs.zHn
	fieldDynamicEntityInformationConfigID     = 3  // HFs.v9n
	fieldDynamicEntityInformationPlayerID     = 4  // HFs.W5n
	fieldDynamicEntityInformationOwnerID      = 5  // HFs.nIs
	fieldDynamicEntityInformationSummonInfo   = 31 // HFs.DKn
	fieldDynamicEntityInformationComponentPbs = 32 // HFs.zEs

	// EntityActiveRequest (ges)
	FieldEntityActiveRequestEntityID = 1 // ges.F4n

	// EntityActiveResponse (fes)
	FieldEntityActiveResponseComponentPbs = 13 // fes.zEs

	// EntityAddNotify (res)
	FieldEntityAddNotifyEntityPbs = 1 // res.zIs

	// EntityCommonTagNotify (Lcs)
	FieldEntityCommonTagNotifyID = 13 // Lcs.s5n

	// EntityComponentPb (m3s)
	FieldEntityComponentPbSummonerComponent             = 4  // m3s.lys
	FieldEntityComponentPbPartComponent                 = 5  // m3s._ys
	FieldEntityComponentPbFightBuffComponent            = 15 // m3s.vys
	FieldEntityComponentPbMonsterCaptureComponent       = 18 // m3s.Sys
	fieldEntityComponentPbConcomitantsComponentPb       = 25 // m3s.Dys
	fieldEntityComponentPbEntityFsmComponentPb          = 28 // m3s.Uys
	fieldEntityComponentPbPlayerFollowerSelfComponentPb = 42 // m3s.tI_
	fieldEntityComponentPbPlayerFollowersComponentPb    = 53 // m3s.nI_
	fieldEntityComponentPbFollowEntityComponentPb       = 85 // m3s.o8m

	// EntityFsmComponentPb (Uys)
	fieldEntityFsmComponentPbHashCode       = 2 // Uys.FTs
	fieldEntityFsmComponentPbCommonHashCode = 3 // Uys.VTs

	// EntityPb (c3s)
	FieldEntityPbID           = 1  // c3s.s5n
	FieldEntityPbConfigID     = 2  // c3s.v9n
	FieldEntityPbConfigType   = 3  // c3s.ZHn
	fieldEntityPbEntityType   = 4  // c3s.zHn
	fieldEntityPbPlayerID     = 10 // c3s.W5n
	FieldEntityPbComponentPbs = 11 // c3s.zEs
	fieldEntityPbPrefabID     = 17 // c3s.LEs
	fieldEntityPbOwnerIncID   = 21 // c3s.JE_
	fieldEntityPbRoleSkinID   = 23 // c3s.eI_

	// EntityRemoveDynamicInteractNotify (Bes)
	FieldEntityRemoveDynamicInteractNotifyEntityID = 12 // Bes.F4n

	// EntityRemoveInfo (Yks)
	FieldEntityRemoveInfoEntityID = 1 // Yks.F4n

	// EntityRemoveNotify (oes)
	FieldEntityRemoveNotifyIsRemove    = 10 // oes.eTs
	FieldEntityRemoveNotifyRemoveInfos = 14 // oes.ZIs

	// EntityStateReadyNotify (Les)
	FieldEntityStateReadyNotifyEntityID = 15 // Les.F4n

	// FightBuffComponentPb (xAs)
	FieldFightBuffComponentPbFightBuffInfos = 1 // xAs.SIs

	// FightBuffInformation (_3s)
	FieldFightBuffInformationHandleID     = 1  // _3s.cVn
	FieldFightBuffInformationBuffID       = 2  // _3s.b6n
	FieldFightBuffInformationLevel        = 3  // _3s.F6n
	FieldFightBuffInformationStackCount   = 4  // _3s.Bjn
	FieldFightBuffInformationInstigatorID = 5  // _3s.Rjn
	FieldFightBuffInformationEntityID     = 6  // _3s.F4n
	FieldFightBuffInformationApplyType    = 7  // _3s.xjn
	FieldFightBuffInformationDuration     = 8  // _3s.n5n
	FieldFightBuffInformationLeftDuration = 9  // _3s.QEs
	FieldFightBuffInformationContext      = 10 // _3s.XEs
	FieldFightBuffInformationIsActive     = 11 // _3s.WHn
	fieldFightBuffInformationServerID     = 12 // _3s.wjn
	fieldFightBuffInformationMessageID    = 13 // _3s.$8n
	FieldFightBuffInformationConfBuffID   = 14 // _3s.tcp

	// FollowEntityComponentPb (o8m)
	fieldFollowEntityComponentPbEntityID = 1 // o8m.F4n

	// GameplayAttributeData ($ks)
	FieldGameplayAttributeDataBaseValue     = 1 // $ks.eSs
	FieldGameplayAttributeDataCurrentValue  = 2 // $ks.y6n
	FieldGameplayAttributeDataAttributeType = 3 // $ks.tSs

	// JoinSceneNotify (Kus)
	fieldJoinSceneNotifySceneInfo = 11 // Kus.$Rs

	// LoginRequest (fss)
	fieldLoginRequestClientBasicInfo = 8 // fss.Z9n

	// MonsterCaptureComponentPb (T3s)
	FieldMonsterCaptureComponentPbTemplateID = 1 // T3s.IIs
	fieldMonsterCaptureComponentPbEntityID   = 2 // T3s.F4n
	FieldMonsterCaptureComponentPbMonsterID  = 3 // T3s.TIs

	// MovePackagePush (Yus)
	FieldMovePackagePushMovingEntities = 5  // Yus.WRs
	FieldMovePackagePushSceneOwnerID   = 13 // Yus.uhh

	// MovingEntityData (r4s)
	FieldMovingEntityDataEntityID   = 1 // r4s.F4n
	FieldMovingEntityDataOriginator = 2 // r4s.Y8n

	// PartComponentPb (BAs)
	FieldPartComponentPbPartLifeInfos = 1 // BAs.PTs

	// PartInformation (lFs)
	FieldPartInformationPartID    = 1 // lFs.jjn
	FieldPartInformationLifeMax   = 3 // lFs.l5n
	FieldPartInformationActivated = 4 // lFs._5n
	FieldPartInformationPartTag   = 5 // lFs.WWn

	// PlayerBattleStateChangeNotify (y3n)
	fieldPlayerBattleStateChangeNotifyPlayerID = 3  // y3n.W5n
	fieldPlayerBattleStateChangeNotifyInBattle = 14 // y3n.iWn

	// PlayerFollowerNotify (Summon.eJs)
	FieldPlayerFollowerNotifyListPlayerFollowerPb = 3 // Summon.eJs.OI_
	FieldPlayerFollowerNotifyPlayerID             = 6 // Summon.eJs.W5n

	// PlayerFollowerPb (Summon.STs)
	FieldPlayerFollowerPbType     = 1 // Summon.STs.h5n
	FieldPlayerFollowerPbEntityID = 2 // Summon.STs.F4n

	// PlayerFollowerSelfComponentPb (Summon.tI_)
	fieldPlayerFollowerSelfComponentPbPlayerEntityID = 1 // Summon.tI_.xIs
	fieldPlayerFollowerSelfComponentPbSummonConfigID = 2 // Summon.tI_.UKn

	// PlayerFollowersComponentPb (Summon.nI_)
	fieldPlayerFollowersComponentPbListPlayerFollowerPb = 1 // Summon.nI_.OI_

	// PlayerSceneAoiData (JFs)
	fieldPlayerSceneAoiDataDynamicEntityList = 1 // JFs.LRs
	FieldPlayerSceneAoiDataEntities          = 3 // JFs.gDs

	// PlayerSceneGroupData (ERs)
	fieldPlayerSceneGroupDataFightRoleInfos = 2 // ERs.gRs

	// PlayerSceneGroupDataList (yRs)
	fieldPlayerSceneGroupDataListPlayerID             = 1 // yRs.W5n
	fieldPlayerSceneGroupDataListPlayerSceneGroupData = 2 // yRs.ERs

	// ProtoKeyResponse (Lss)
	fieldProtoKeyResponseType = 2 // Lss.h5n
	fieldProtoKeyResponseKey  = 3 // Lss.Z4n

	// RTimeStopPush (Fe_)
	fieldRTimeStopPushDuration = 1  // Fe_.n5n
	fieldRTimeStopPushDilation = 12 // Fe_.A6n

	// RecoverAttr (N4s)
	FieldRecoverAttrAttrID       = 1 // N4s.E6n
	FieldRecoverAttrRatio        = 2 // N4s.L6n
	FieldRecoverAttrMaxValue     = 4 // N4s.I6n
	FieldRecoverAttrCurrentValue = 5 // N4s.y6n

	// RecoverPropChangedNotify (v3n)
	FieldRecoverPropChangedNotifyAttributes = 14 // v3n.GSs

	// RemoveBuffByIdS2cRequestNotify (M3n)
	FieldRemoveBuffByIDS2cRequestNotifyInstigatorID = 4  // M3n.Rjn
	FieldRemoveBuffByIDS2cRequestNotifyBuffID       = 8  // M3n.b6n
	fieldRemoveBuffByIDS2cRequestNotifyReason       = 13 // M3n.x9n
	fieldRemoveBuffByIDS2cRequestNotifyStackCount   = 14 // M3n.Bjn

	// RemoveBuffByIdS2cResponsePush (N4n)
	fieldRemoveBuffByIDS2cResponsePushErrorCode = 13 // N4n.Q4n

	// RemoveBuffS2cRequestNotify (C3n)
	fieldRemoveBuffS2cRequestNotifyStackCount   = 1  // C3n.Bjn
	fieldRemoveBuffS2cRequestNotifyHandle       = 9  // C3n.uVn
	fieldRemoveBuffS2cRequestNotifyReason       = 11 // C3n.x9n
	fieldRemoveBuffS2cRequestNotifyInstigatorID = 15 // C3n.Rjn

	// RemoveGameplayEffectNotify (IFn)
	FieldRemoveGameplayEffectNotifyHandle       = 1  // IFn.uVn
	FieldRemoveGameplayEffectNotifyInstigatorID = 7  // IFn.Rjn
	FieldRemoveGameplayEffectNotifyEntityID     = 10 // IFn.F4n

	// RemoveGameplayEffectPush (re_)
	FieldRemoveGameplayEffectPushIsPrematureRemoval = 1  // re_.Ojn
	fieldRemoveGameplayEffectPushReason             = 5  // re_.x9n
	FieldRemoveGameplayEffectPushHandle             = 8  // re_.uVn
	fieldRemoveGameplayEffectPushInstigatorID       = 10 // re_.Rjn
	FieldRemoveGameplayEffectPushEntityID           = 14 // re_.F4n

	// SceneInformation (YFs)
	fieldSceneInformationInstanceID        = 2  // YFs.d5n
	fieldSceneInformationPlayerInfos       = 4  // YFs.TRs
	fieldSceneInformationDynamicEntityList = 5  // YFs.LRs
	fieldSceneInformationAoiData           = 11 // YFs.RRs
	fieldSceneInformationSceneEntityID     = 24 // YFs.tla

	// ScenePlayerInformation (WFs)
	fieldScenePlayerInformationPlayerID             = 1  // WFs.W5n
	fieldScenePlayerInformationPlayerSceneGroupData = 11 // WFs.ERs

	// SceneRoleInformation (KFs)
	fieldSceneRoleInformationRoleID   = 1 // KFs.Q6n
	fieldSceneRoleInformationEntityID = 2 // KFs.F4n

	// SimpleCombatEntityBuffLayerCountNotify (J0d)
	fieldSimpleCombatEntityBuffLayerCountNotifyLayerCount = 1 // J0d.Z0d
	fieldSimpleCombatEntityBuffLayerCountNotifyBuffID     = 5 // J0d.b6n
	fieldSimpleCombatEntityBuffLayerCountNotifyEntityID   = 8 // J0d.F4n

	// SimpleCombatEntityBuffUpdateNotify (Zju)
	fieldSimpleCombatEntityBuffUpdateNotifyIsBuffAdd = 3  // Zju.t9u
	fieldSimpleCombatEntityBuffUpdateNotifyBuffID    = 11 // Zju.b6n
	FieldSimpleCombatEntityBuffUpdateNotifyEntityID  = 15 // Zju.F4n

	// SummonInfo (DKn)
	fieldSummonInfoSummonCfgID   = 1 // DKn.dIs
	fieldSummonInfoSummonerID    = 2 // DKn.YWn
	fieldSummonInfoSummonSkillID = 3 // DKn.mIs

	// SummonerComponentPb (Summon.p3s)
	FieldSummonerComponentPbSummonerID    = 1 // Summon.p3s.YWn
	FieldSummonerComponentPbSummonCfgID   = 2 // Summon.p3s.dIs
	fieldSummonerComponentPbSummonSkillID = 3 // Summon.p3s.mIs
	fieldSummonerComponentPbPlayerID      = 4 // Summon.p3s.W5n
	FieldSummonerComponentPbType          = 5 // Summon.p3s.h5n

	// TimeCheckRequest (WCs)
	fieldTimeCheckRequestFlowTimeDilation = 7  // WCs.U6n
	fieldTimeCheckRequestTimeDilation     = 15 // WCs.A6n

	// TimeStopPush (GCs)
	fieldTimeStopPushTimeDilation = 11 // GCs.dKn

	// UpdateSceneGroupDataNotify (Hgs)
	fieldUpdateSceneGroupDataNotifyPlayerSceneGroupDataList = 12 // Hgs.yRs
)
