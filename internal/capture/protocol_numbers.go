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
	cmdLoginRequest                            = 103   // fss
	cmdProtoKeyResponse                        = 112   // Lss
	cmdCheckClientPush                         = 116   // Roa
	cmdAtomicProcessAddBuffResponse            = 15504 // Rxf
	cmdRemoveBuffByServerIDS2cRequestNotify    = 15505 // rpm
	cmdRemoveBuffByIDS2cResponsePush           = 15939 // N4n
	cmdOrderApplyBuffResponse                  = 15979 // uzn
	cmdRemoveGameplayEffectRequest             = 15998 // x3n
	cmdActivateBuffPush                        = 16028 // pe_
	cmdRemoveBuffS2cResponsePush               = 16300 // O4n
	cmdBuffStackCountResponse                  = 16438 // fzn
	cmdSimpleCombatEntityAttributeUpdateNotify = 16636 // ecC
	cmdTimeStopPush                            = 16707 // GCs
	cmdUpdatePlayersFightRolesInfoNotify       = 16745 // Ous
	cmdApplyBuffS2cResponsePush                = 17435 // G4n
	cmdApplyGameplayEffectPush                 = 17445 // ie_
	cmdApplyBuffS2cRequestNotify               = 17614 // d3n
	cmdOrderRemoveBuffResponse                 = 17733 // czn
	cmdBuffStackCountPush                      = 17747 // Re_
	cmdEntityRemoveNotify                      = 17927 // oes
	cmdRemoveGameplayEffectNotify              = 18172 // IFn
	cmdSimpleCombatEntityBuffUpdateNotify      = 18273 // Zju
	cmdEntityRemoveDynamicInteractNotify       = 18312 // Bes
	cmdBuffEffectExecutePush                   = 18599 // Vzd
	cmdDamageExecuteRequest                    = 18654 // U3n
	cmdMovePackagePush                         = 18855 // Yus
	cmdEntityBuffProducerRequest               = 18892 // _es
	cmdEntityBuffProducerResponse              = 18914 // ues
	cmdActivateBuffResponse                    = 19166 // dzn
	cmdAddSysBuffNotify                        = 19225 // Hcs
	cmdAtomicProcessRemoveBuffRequest          = 19269 // hMg
	cmdBuffStackCountNotify                    = 19317 // i3n
	cmdBuffEffectExecuteRequest                = 19635 // Czn
	cmdEntityActiveResponse                    = 19734 // fes
	cmdOrderApplyBuffNotify                    = 19792 // HFn
	cmdEntityCommonTagNotify                   = 20031 // Lcs
	cmdBuffEffectResponse                      = 20053 // Ezn
	cmdOrderRemoveBuffByTagsRequest            = 20210 // Y3n
	cmdEntityAddNotify                         = 20302 // res
	cmdOrderRemoveBuffByTagsResponse           = 20335 // mzn
	cmdJoinSceneNotify                         = 20597 // Kus
	cmdUpdatePlayerAllFightRoleNotify          = 20791 // Gus
	cmdOrderRemoveBuffNotify                   = 20935 // jFn
	cmdOrderApplyBuffRequest                   = 21297 // Q3n
	cmdCombatSendPackResponse                  = 21536 // CombatMessage.aZn
	cmdTransformBuffStackNotify                = 21805 // x9f
	cmdBasicInfoNotify                         = 21993 // hYn
	cmdUpdateSceneGroupDataNotify              = 22332 // Hgs
	cmdBuffEffectRequest                       = 22402 // I4n
	cmdEntityBuffProducerOperateResponse       = 22978 // des
	cmdRemoveSysBuffNotify                     = 23366 // Wcs
	cmdOrderRemoveBuffByTagsNotify             = 23871 // KFn
	cmdApplyGameplayEffectResponse             = 23904 // lzn
	cmdUpdateSysBuffNotify                     = 23927 // jcs
	cmdBuffStackCountRequest                   = 24150 // u4n
	cmdSimpleCombatEntitySyncAttributePush     = 24434 // pGd
	cmdBuffDurationNotify                      = 24676 // W7l
	cmdAtomicProcessRemoveBuffResponse         = 24883 // lMg
	cmdRefreshBuffDurationPush                 = 24982 // GG1
	cmdEntityBuffProducerOperateRequest        = 25261 // ces
	cmdRemoveBuffByIDS2cRequestNotify          = 25284 // M3n
	cmdChangePlayerFightRoleNotify             = 25527 // Nus
	cmdApplyGameplayEffectNotify               = 26040 // yFn
	cmdEntityActiveRequest                     = 26157 // ges
	cmdPlayerLivingStatesNotify                = 26412 // w7s
	cmdCombatSendPackRequest                   = 26497 // CombatMessage.sZn
	cmdBuffEffectPush                          = 26677 // Ge_
	cmdOrderRemoveBuffRequest                  = 26903 // X3n
	cmdApplyGameplayEffectRequest              = 26992 // R3n
	cmdEntityStateReadyNotify                  = 27022 // Les
	cmdPlayerFollowerNotify                    = 27250 // Summon.eJs
	cmdActivateBuffRequest                     = 27416 // $3n
	cmdAtomicProcessAddBuffRequest             = 27678 // wxf
	cmdUpdateTimerInfoNotify                   = 27863 // AJn
	cmdBuffEffectExecuteResponse               = 27923 // gzn
	cmdTimeCheckResponse                       = 28031 // QCs
	cmdRemoveBuffS2cRequestNotify              = 28392 // C3n
	cmdTimeCheckRequest                        = 28799 // WCs
	cmdUpdatePlayerSingleFightRoleNotify       = 28925 // kus
	cmdSimpleCombatEntityBuffLayerCountNotify  = 29187 // J0d
	cmdRemoveGameplayEffectPush                = 29542 // re_
	cmdPhantomAttrRecommendResponse            = 29590 // Yv_
	cmdActivateBuffNotify                      = 29719 // WFn
	cmdRemoveGameplayEffectResponse            = 29948 // _zn
	cmdCombatReceivePackNotify                 = 29973 // CombatMessage.hZn
)

// Fields of Global 3.8.9, by message; each comment is the client's minified message and property name.
const (
	// ActivateBuffNotify (WFn)
	fieldActivateBuffNotifyOn     = 2 // WFn.qjn
	fieldActivateBuffNotifyHandle = 7 // WFn.uVn

	// ApplyBuffS2cRequestNotify (d3n)
	fieldApplyBuffS2cRequestNotifyInstigatorID = 3  // d3n.Rjn
	fieldApplyBuffS2cRequestNotifyReason       = 4  // d3n.x9n
	fieldApplyBuffS2cRequestNotifyID           = 5  // d3n.s5n
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
	fieldApplyGameplayEffectNotifyDuration     = 1  // yFn.n5n
	fieldApplyGameplayEffectNotifyServerID     = 2  // yFn.wjn
	fieldApplyGameplayEffectNotifyLeftDuration = 3  // yFn.QEs
	fieldApplyGameplayEffectNotifyApplyType    = 4  // yFn.xjn
	fieldApplyGameplayEffectNotifyHandle       = 6  // yFn.uVn
	fieldApplyGameplayEffectNotifyIsActive     = 7  // yFn.WHn
	fieldApplyGameplayEffectNotifyInstigatorID = 9  // yFn.Rjn
	fieldApplyGameplayEffectNotifyStackCount   = 10 // yFn.Bjn
	fieldApplyGameplayEffectNotifyLevel        = 11 // yFn.F6n
	fieldApplyGameplayEffectNotifyConfBuffID   = 12 // yFn.tcp
	fieldApplyGameplayEffectNotifyID           = 13 // yFn.s5n
	fieldApplyGameplayEffectNotifyEntityID     = 14 // yFn.F4n

	// ApplyGameplayEffectPush (ie_)
	fieldApplyGameplayEffectPushReason       = 1  // ie_.x9n
	fieldApplyGameplayEffectPushServerID     = 3  // ie_.wjn
	fieldApplyGameplayEffectPushIsActive     = 4  // ie_.WHn
	fieldApplyGameplayEffectPushApplyType    = 6  // ie_.xjn
	fieldApplyGameplayEffectPushLevel        = 7  // ie_.F6n
	fieldApplyGameplayEffectPushHandle       = 8  // ie_.uVn
	fieldApplyGameplayEffectPushInstigatorID = 10 // ie_.Rjn
	fieldApplyGameplayEffectPushDuration     = 11 // ie_.n5n
	fieldApplyGameplayEffectPushStackCount   = 12 // ie_.Bjn
	fieldApplyGameplayEffectPushConfBuffID   = 14 // ie_.tcp
	fieldApplyGameplayEffectPushID           = 15 // ie_.s5n

	// AttributeChangedNotify (OFn)
	fieldAttributeChangedNotifyAttributes = 6 // OFn.GSs

	// BasicInfoNotify (hYn)
	fieldBasicInfoNotifyID = 4 // hYn.s5n

	// BuffDurationNotify (W7l)
	fieldBuffDurationNotifyLeftDuration = 6  // W7l.QEs
	fieldBuffDurationNotifyHandleID     = 8  // W7l.cVn
	fieldBuffDurationNotifyDuration     = 13 // W7l.n5n

	// BuffStackCountNotify (i3n)
	fieldBuffStackCountNotifyLeftDuration       = 2  // i3n.QEs
	fieldBuffStackCountNotifyHandleID           = 5  // i3n.cVn
	fieldBuffStackCountNotifyNewStackCount      = 6  // i3n.Gjn
	fieldBuffStackCountNotifyNotRefreshDuration = 7  // i3n.$L1
	fieldBuffStackCountNotifyInstigatorID       = 10 // i3n.Rjn
	fieldBuffStackCountNotifyDuration           = 13 // i3n.n5n
	fieldBuffStackCountNotifyNotRefreshPeriod   = 15 // i3n.WL1

	// BuffStackCountPush (Re_)
	fieldBuffStackCountPushNotRefreshPeriod   = 1  // Re_.WL1
	fieldBuffStackCountPushIsPrematureRemoval = 2  // Re_.Ojn
	fieldBuffStackCountPushNotRefreshDuration = 3  // Re_.$L1
	fieldBuffStackCountPushDuration           = 5  // Re_.n5n
	fieldBuffStackCountPushReason             = 7  // Re_.x9n
	fieldBuffStackCountPushNewStackCount      = 8  // Re_.Gjn
	fieldBuffStackCountPushHandleID           = 9  // Re_.cVn
	fieldBuffStackCountPushInstigatorID       = 10 // Re_.Rjn

	// ChangeStateNotify (e3n)
	fieldChangeStateNotifyToState   = 4  // e3n.z4n
	fieldChangeStateNotifyFromState = 7  // e3n.J4n
	fieldChangeStateNotifyFsmID     = 12 // e3n.$4n

	// ClientBasicInfo (Z9n)
	fieldClientBasicInfoClientVersion = 19 // Z9n.cjf

	// ClientCurrentRoleReportPush (Xe_)
	fieldClientCurrentRoleReportPushCurrentEntityID = 10 // Xe_.JHn

	// CombatCommon (K8n)
	fieldCombatCommonPreMessageID = 1 // K8n.X8n
	fieldCombatCommonMessageID    = 2 // K8n.$8n
	fieldCombatCommonOriginator   = 3 // K8n.Y8n
	fieldCombatCommonTimeStamp    = 4 // K8n.J8n
	fieldCombatCommonEntityID     = 5 // K8n.F4n

	// CombatNotifyData (CombatMessage.KLs)
	fieldCombatNotifyDataCombatCommon                   = 1  // CombatMessage.KLs.K8n
	fieldCombatNotifyDataDamageExecuteNotify            = 4  // CombatMessage.KLs.EFn
	fieldCombatNotifyDataApplyGameplayEffectNotify      = 5  // CombatMessage.KLs.yFn
	fieldCombatNotifyDataRemoveGameplayEffectNotify     = 6  // CombatMessage.KLs.IFn
	fieldCombatNotifyDataAttributeChangedNotify         = 22 // CombatMessage.KLs.OFn
	fieldCombatNotifyDataActivateBuffNotify             = 29 // CombatMessage.KLs.WFn
	fieldCombatNotifyDataChangeStateNotify              = 38 // CombatMessage.KLs.e3n
	fieldCombatNotifyDataBuffStackCountNotify           = 41 // CombatMessage.KLs.i3n
	fieldCombatNotifyDataApplyBuffS2CRequestNotify      = 53 // CombatMessage.KLs.d3n
	fieldCombatNotifyDataRemoveBuffS2CRequestNotify     = 54 // CombatMessage.KLs.C3n
	fieldCombatNotifyDataRecoverPropChangedNotify       = 58 // CombatMessage.KLs.v3n
	fieldCombatNotifyDataRemoveBuffByIDS2CRequestNotify = 59 // CombatMessage.KLs.M3n
	fieldCombatNotifyDataPlayerBattleStateChangeNotify  = 62 // CombatMessage.KLs.y3n
	fieldCombatNotifyDataBuffDurationNotify             = 73 // CombatMessage.KLs.W7l

	// CombatPushData (CombatMessage.OFs)
	fieldCombatPushDataCombatCommon                  = 1  // CombatMessage.OFs.K8n
	fieldCombatPushDataApplyBuffS2CResponsePush      = 4  // CombatMessage.OFs.G4n
	fieldCombatPushDataRemoveBuffByIDS2CResponsePush = 6  // CombatMessage.OFs.N4n
	fieldCombatPushDataApplyGameplayEffectPush       = 9  // CombatMessage.OFs.ie_
	fieldCombatPushDataRemoveGameplayEffectPush      = 10 // CombatMessage.OFs.re_
	fieldCombatPushDataBuffStackCountPush            = 37 // CombatMessage.OFs.Re_
	fieldCombatPushDataRTimeStopPush                 = 47 // CombatMessage.OFs.Fe_
	fieldCombatPushDataClientCurrentRoleReportPush   = 56 // CombatMessage.OFs.Xe_

	// CombatReceiveData (CombatMessage.$Fs)
	fieldCombatReceiveDataCombatNotifyData   = 2 // CombatMessage.$Fs.KLs
	fieldCombatReceiveDataCombatResponseData = 3 // CombatMessage.$Fs.QLs

	// CombatReceivePackNotify (CombatMessage.hZn)
	fieldCombatReceivePackNotifyData = 4 // CombatMessage.hZn.R5n

	// CombatRequestData (CombatMessage.FFs)
	fieldCombatRequestDataRequestID            = 2 // CombatMessage.FFs.W8n
	fieldCombatRequestDataDamageExecuteRequest = 5 // CombatMessage.FFs.U3n

	// CombatResponseData (CombatMessage.QLs)
	fieldCombatResponseDataRequestID             = 2 // CombatMessage.QLs.W8n
	fieldCombatResponseDataDamageExecuteResponse = 5 // CombatMessage.QLs.lis

	// CombatSendData (CombatMessage.VFs)
	fieldCombatSendDataPush    = 2 // CombatMessage.VFs.Q8n
	fieldCombatSendDataRequest = 3 // CombatMessage.VFs.x5n

	// CombatSendPackRequest (CombatMessage.sZn)
	fieldCombatSendPackRequestData = 11 // CombatMessage.sZn.R5n

	// CombatSendPackResponse (CombatMessage.aZn)
	fieldCombatSendPackResponseReceivePackNotify = 1 // CombatMessage.aZn.XLs

	// ConcomitantsComponentPb (Dys)
	fieldConcomitantsComponentPbVisionEntityIDs = 1 // Dys.wzm
	fieldConcomitantsComponentPbCustomEntityIDs = 2 // Dys.OIs
	fieldConcomitantsComponentPbPhantomRoleEid  = 3 // Dys.kIs
	fieldConcomitantsComponentPbWeakVisionEid   = 4 // Dys.Vnm

	// DamageContext (Njn)
	fieldDamageContextSourceType     = 1 // Njn.Vjn
	fieldDamageContextBulletID       = 2 // Njn.Mjn
	fieldDamageContextBulletTags     = 3 // Njn.Hjn
	fieldDamageContextSkillID        = 4 // Njn.r5n
	fieldDamageContextSkillMessageID = 5 // Njn.ptc

	// DamageExecuteNotify (EFn)
	fieldDamageExecuteNotifyChangeLife        = 1  // EFn.jQ_
	fieldDamageExecuteNotifyTargetEntityID    = 2  // EFn.TVn
	fieldDamageExecuteNotifyIsCrit            = 3  // EFn.sAs
	fieldDamageExecuteNotifyElementType       = 4  // EFn.wHn
	fieldDamageExecuteNotifyDamageContext     = 5  // EFn.Njn
	fieldDamageExecuteNotifyShieldCoverDamage = 6  // EFn.hAs
	fieldDamageExecuteNotifyDamage            = 7  // EFn.nAs
	fieldDamageExecuteNotifyKillerEntityID    = 8  // EFn.GUp
	fieldDamageExecuteNotifyChangeWeakness    = 9  // EFn.ekf
	fieldDamageExecuteNotifyAttackerEntityID  = 10 // EFn.kjn
	fieldDamageExecuteNotifyPartID            = 11 // EFn.jjn
	fieldDamageExecuteNotifyDamageID          = 12 // EFn.Fjn
	fieldDamageExecuteNotifySkillLevel        = 13 // EFn.Wjn
	fieldDamageExecuteNotifyKilledTarget      = 14 // EFn.aAs
	fieldDamageExecuteNotifyImmuneType        = 15 // EFn.lAs

	// DamageExecuteRequest (U3n)
	fieldDamageExecuteRequestTargetEntityID   = 1  // U3n.TVn
	fieldDamageExecuteRequestDamageContext    = 2  // U3n.Njn
	fieldDamageExecuteRequestAttackerEntityID = 5  // U3n.kjn
	fieldDamageExecuteRequestSkillLevel       = 7  // U3n.Wjn
	fieldDamageExecuteRequestPartID           = 10 // U3n.jjn
	fieldDamageExecuteRequestDamageID         = 14 // U3n.Fjn

	// DamageExecuteResponse (lis)
	fieldDamageExecuteResponseErrorCode  = 5 // lis.Q4n
	fieldDamageExecuteResponseChangeLife = 6 // lis.jQ_
	fieldDamageExecuteResponseImmuneType = 7 // lis.lAs
	fieldDamageExecuteResponseDamage     = 9 // lis.nAs

	// DynamicEntityInformation (HFs)
	fieldDynamicEntityInformationID           = 1  // HFs.s5n
	fieldDynamicEntityInformationEntityType   = 2  // HFs.zHn
	fieldDynamicEntityInformationConfigID     = 3  // HFs.v9n
	fieldDynamicEntityInformationPlayerID     = 4  // HFs.W5n
	fieldDynamicEntityInformationOwnerID      = 5  // HFs.nIs
	fieldDynamicEntityInformationSummonInfo   = 31 // HFs.DKn
	fieldDynamicEntityInformationComponentPbs = 32 // HFs.zEs

	// EntityActiveRequest (ges)
	fieldEntityActiveRequestEntityID = 1 // ges.F4n

	// EntityActiveResponse (fes)
	fieldEntityActiveResponseComponentPbs = 13 // fes.zEs

	// EntityAddNotify (res)
	fieldEntityAddNotifyEntityPbs = 1 // res.zIs

	// EntityCommonTagNotify (Lcs)
	fieldEntityCommonTagNotifyID = 13 // Lcs.s5n

	// EntityComponentPb (m3s)
	fieldEntityComponentPbSummonerComponent             = 4  // m3s.lys
	fieldEntityComponentPbPartComponent                 = 5  // m3s._ys
	fieldEntityComponentPbFightBuffComponent            = 15 // m3s.vys
	fieldEntityComponentPbMonsterCaptureComponent       = 18 // m3s.Sys
	fieldEntityComponentPbConcomitantsComponentPb       = 25 // m3s.Dys
	fieldEntityComponentPbEntityFsmComponentPb          = 28 // m3s.Uys
	fieldEntityComponentPbPlayerFollowerSelfComponentPb = 42 // m3s.tI_
	fieldEntityComponentPbPlayerFollowersComponentPb    = 53 // m3s.nI_
	fieldEntityComponentPbFollowEntityComponentPb       = 85 // m3s.o8m

	// EntityFsmComponentPb (Uys)
	fieldEntityFsmComponentPbHashCode       = 2 // Uys.FTs
	fieldEntityFsmComponentPbCommonHashCode = 3 // Uys.VTs

	// EntityPb (c3s)
	fieldEntityPbID           = 1  // c3s.s5n
	fieldEntityPbConfigID     = 2  // c3s.v9n
	fieldEntityPbConfigType   = 3  // c3s.ZHn
	fieldEntityPbEntityType   = 4  // c3s.zHn
	fieldEntityPbPlayerID     = 10 // c3s.W5n
	fieldEntityPbComponentPbs = 11 // c3s.zEs
	fieldEntityPbPrefabID     = 17 // c3s.LEs
	fieldEntityPbOwnerIncID   = 21 // c3s.JE_
	fieldEntityPbRoleSkinID   = 23 // c3s.eI_

	// EntityRemoveDynamicInteractNotify (Bes)
	fieldEntityRemoveDynamicInteractNotifyEntityID = 12 // Bes.F4n

	// EntityRemoveInfo (Yks)
	fieldEntityRemoveInfoEntityID = 1 // Yks.F4n

	// EntityRemoveNotify (oes)
	fieldEntityRemoveNotifyIsRemove    = 10 // oes.eTs
	fieldEntityRemoveNotifyRemoveInfos = 14 // oes.ZIs

	// EntityStateReadyNotify (Les)
	fieldEntityStateReadyNotifyEntityID = 15 // Les.F4n

	// FightBuffComponentPb (xAs)
	fieldFightBuffComponentPbFightBuffInfos = 1 // xAs.SIs

	// FightBuffInformation (_3s)
	fieldFightBuffInformationHandleID     = 1  // _3s.cVn
	fieldFightBuffInformationBuffID       = 2  // _3s.b6n
	fieldFightBuffInformationLevel        = 3  // _3s.F6n
	fieldFightBuffInformationStackCount   = 4  // _3s.Bjn
	fieldFightBuffInformationInstigatorID = 5  // _3s.Rjn
	fieldFightBuffInformationEntityID     = 6  // _3s.F4n
	fieldFightBuffInformationApplyType    = 7  // _3s.xjn
	fieldFightBuffInformationDuration     = 8  // _3s.n5n
	fieldFightBuffInformationLeftDuration = 9  // _3s.QEs
	fieldFightBuffInformationContext      = 10 // _3s.XEs
	fieldFightBuffInformationIsActive     = 11 // _3s.WHn
	fieldFightBuffInformationServerID     = 12 // _3s.wjn
	fieldFightBuffInformationMessageID    = 13 // _3s.$8n
	fieldFightBuffInformationConfBuffID   = 14 // _3s.tcp

	// FollowEntityComponentPb (o8m)
	fieldFollowEntityComponentPbEntityID = 1 // o8m.F4n

	// GameplayAttributeData ($ks)
	fieldGameplayAttributeDataBaseValue     = 1 // $ks.eSs
	fieldGameplayAttributeDataCurrentValue  = 2 // $ks.y6n
	fieldGameplayAttributeDataAttributeType = 3 // $ks.tSs

	// JoinSceneNotify (Kus)
	fieldJoinSceneNotifySceneInfo = 11 // Kus.$Rs

	// LoginRequest (fss)
	fieldLoginRequestClientBasicInfo = 8 // fss.Z9n

	// MonsterCaptureComponentPb (T3s)
	fieldMonsterCaptureComponentPbTemplateID = 1 // T3s.IIs
	fieldMonsterCaptureComponentPbEntityID   = 2 // T3s.F4n
	fieldMonsterCaptureComponentPbMonsterID  = 3 // T3s.TIs

	// MovePackagePush (Yus)
	fieldMovePackagePushMovingEntities = 5  // Yus.WRs
	fieldMovePackagePushSceneOwnerID   = 13 // Yus.uhh

	// MovingEntityData (r4s)
	fieldMovingEntityDataEntityID   = 1 // r4s.F4n
	fieldMovingEntityDataOriginator = 2 // r4s.Y8n

	// PartComponentPb (BAs)
	fieldPartComponentPbPartLifeInfos = 1 // BAs.PTs

	// PartInformation (lFs)
	fieldPartInformationPartID    = 1 // lFs.jjn
	fieldPartInformationLifeMax   = 3 // lFs.l5n
	fieldPartInformationActivated = 4 // lFs._5n
	fieldPartInformationPartTag   = 5 // lFs.WWn

	// PlayerBattleStateChangeNotify (y3n)
	fieldPlayerBattleStateChangeNotifyPlayerID = 3  // y3n.W5n
	fieldPlayerBattleStateChangeNotifyInBattle = 14 // y3n.iWn

	// PlayerFollowerNotify (Summon.eJs)
	fieldPlayerFollowerNotifyListPlayerFollowerPb = 3 // Summon.eJs.OI_
	fieldPlayerFollowerNotifyPlayerID             = 6 // Summon.eJs.W5n

	// PlayerFollowerPb (Summon.STs)
	fieldPlayerFollowerPbType     = 1 // Summon.STs.h5n
	fieldPlayerFollowerPbEntityID = 2 // Summon.STs.F4n

	// PlayerFollowerSelfComponentPb (Summon.tI_)
	fieldPlayerFollowerSelfComponentPbPlayerEntityID = 1 // Summon.tI_.xIs
	fieldPlayerFollowerSelfComponentPbSummonConfigID = 2 // Summon.tI_.UKn

	// PlayerFollowersComponentPb (Summon.nI_)
	fieldPlayerFollowersComponentPbListPlayerFollowerPb = 1 // Summon.nI_.OI_

	// PlayerSceneAoiData (JFs)
	fieldPlayerSceneAoiDataDynamicEntityList = 1 // JFs.LRs
	fieldPlayerSceneAoiDataEntities          = 3 // JFs.gDs

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
	fieldRecoverAttrAttrID       = 1 // N4s.E6n
	fieldRecoverAttrRatio        = 2 // N4s.L6n
	fieldRecoverAttrMaxValue     = 4 // N4s.I6n
	fieldRecoverAttrCurrentValue = 5 // N4s.y6n

	// RecoverPropChangedNotify (v3n)
	fieldRecoverPropChangedNotifyAttributes = 14 // v3n.GSs

	// RemoveBuffByIdS2cRequestNotify (M3n)
	fieldRemoveBuffByIDS2cRequestNotifyInstigatorID = 4  // M3n.Rjn
	fieldRemoveBuffByIDS2cRequestNotifyBuffID       = 8  // M3n.b6n
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
	fieldRemoveGameplayEffectNotifyHandle       = 1  // IFn.uVn
	fieldRemoveGameplayEffectNotifyInstigatorID = 7  // IFn.Rjn
	fieldRemoveGameplayEffectNotifyEntityID     = 10 // IFn.F4n

	// RemoveGameplayEffectPush (re_)
	fieldRemoveGameplayEffectPushIsPrematureRemoval = 1  // re_.Ojn
	fieldRemoveGameplayEffectPushReason             = 5  // re_.x9n
	fieldRemoveGameplayEffectPushHandle             = 8  // re_.uVn
	fieldRemoveGameplayEffectPushInstigatorID       = 10 // re_.Rjn
	fieldRemoveGameplayEffectPushEntityID           = 14 // re_.F4n

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
	fieldSimpleCombatEntityBuffUpdateNotifyEntityID  = 15 // Zju.F4n

	// SummonInfo (DKn)
	fieldSummonInfoSummonCfgID   = 1 // DKn.dIs
	fieldSummonInfoSummonerID    = 2 // DKn.YWn
	fieldSummonInfoSummonSkillID = 3 // DKn.mIs

	// SummonerComponentPb (Summon.p3s)
	fieldSummonerComponentPbSummonerID    = 1 // Summon.p3s.YWn
	fieldSummonerComponentPbSummonCfgID   = 2 // Summon.p3s.dIs
	fieldSummonerComponentPbSummonSkillID = 3 // Summon.p3s.mIs
	fieldSummonerComponentPbPlayerID      = 4 // Summon.p3s.W5n
	fieldSummonerComponentPbType          = 5 // Summon.p3s.h5n

	// TimeCheckRequest (WCs)
	fieldTimeCheckRequestFlowTimeDilation = 7  // WCs.U6n
	fieldTimeCheckRequestTimeDilation     = 15 // WCs.A6n

	// TimeStopPush (GCs)
	fieldTimeStopPushTimeDilation = 11 // GCs.dKn

	// UpdateSceneGroupDataNotify (Hgs)
	fieldUpdateSceneGroupDataNotifyPlayerSceneGroupDataList = 12 // Hgs.yRs
)
