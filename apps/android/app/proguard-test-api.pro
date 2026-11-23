# CatalogCheck is a separately compiled client of the final, optimized APK.
# R8 TraceReferences --keep-rules --allowobfuscation, from androidTest against the
# release classes and runtime jars. Keep only its referenced ABI.
-keep,allowobfuscation class androidx.activity.ComponentActivity {
  public androidx.lifecycle.Lifecycle getLifecycle();
}
-keep,allowobfuscation class androidx.activity.compose.ComponentActivityKt {
  public static void setContent$default(androidx.activity.ComponentActivity,androidx.compose.runtime.CompositionContext,kotlin.jvm.functions.Function2,int,java.lang.Object);
}
-keep,allowobfuscation class androidx.compose.foundation.BorderStroke {
}
-keep,allowobfuscation class androidx.compose.foundation.ScrollKt {
  public static androidx.compose.foundation.ScrollState rememberScrollState(int,androidx.compose.runtime.Composer,int,int);
  public static androidx.compose.ui.Modifier verticalScroll$default(androidx.compose.ui.Modifier,androidx.compose.foundation.ScrollState,boolean,androidx.compose.foundation.gestures.FlingBehavior,boolean,int,java.lang.Object);
}
-keep,allowobfuscation class androidx.compose.foundation.ScrollState {
}
-keep,allowobfuscation interface androidx.compose.foundation.gestures.FlingBehavior {
}
-keep,allowobfuscation class androidx.compose.foundation.layout.Arrangement {
  public androidx.compose.foundation.layout.Arrangement$HorizontalOrVertical spacedBy-0680j_4(float);
  androidx.compose.foundation.layout.Arrangement INSTANCE;
}
-keep,allowobfuscation interface androidx.compose.foundation.layout.Arrangement$HorizontalOrVertical {
}
-keep,allowobfuscation interface androidx.compose.foundation.layout.Arrangement$Vertical {
}
-keep,allowobfuscation class androidx.compose.foundation.layout.ColumnKt {
  public static androidx.compose.ui.layout.MeasurePolicy columnMeasurePolicy(androidx.compose.foundation.layout.Arrangement$Vertical,androidx.compose.ui.Alignment$Horizontal,androidx.compose.runtime.Composer,int);
}
-keep,allowobfuscation interface androidx.compose.foundation.layout.ColumnScope {
}
-keep,allowobfuscation class androidx.compose.foundation.layout.ColumnScopeInstance {
  androidx.compose.foundation.layout.ColumnScopeInstance INSTANCE;
}
-keep,allowobfuscation class androidx.compose.foundation.layout.PaddingKt {
  public static androidx.compose.ui.Modifier padding-3ABfNKs(androidx.compose.ui.Modifier,float);
}
-keep,allowobfuscation interface androidx.compose.foundation.layout.RowScope {
}
-keep,allowobfuscation class androidx.compose.foundation.layout.SizeKt {
  public static androidx.compose.ui.Modifier fillMaxSize$default(androidx.compose.ui.Modifier,float,int,java.lang.Object);
  public static androidx.compose.ui.Modifier fillMaxWidth$default(androidx.compose.ui.Modifier,float,int,java.lang.Object);
}
-keep,allowobfuscation interface androidx.compose.foundation.text.TextAutoSize {
}
-keep,allowobfuscation class androidx.compose.material3.SurfaceKt {
  public static void Surface-T9BRK9s(androidx.compose.ui.Modifier,androidx.compose.ui.graphics.Shape,long,long,float,float,androidx.compose.foundation.BorderStroke,kotlin.jvm.functions.Function2,androidx.compose.runtime.Composer,int,int);
}
-keep,allowobfuscation class androidx.compose.material3.TextKt {
  public static void Text-Nvy7gAk(java.lang.String,androidx.compose.ui.Modifier,long,androidx.compose.foundation.text.TextAutoSize,long,androidx.compose.ui.text.font.FontStyle,androidx.compose.ui.text.font.FontWeight,androidx.compose.ui.text.font.FontFamily,long,androidx.compose.ui.text.style.TextDecoration,androidx.compose.ui.text.style.TextAlign,long,int,boolean,int,int,kotlin.jvm.functions.Function1,androidx.compose.ui.text.TextStyle,androidx.compose.runtime.Composer,int,int,int);
}
-keep,allowobfuscation interface androidx.compose.runtime.Applier {
}
-keep,allowobfuscation @interface androidx.compose.runtime.Composable {
}
-keep,allowobfuscation @interface androidx.compose.runtime.ComposableTarget {
  public java.lang.String applier();
}
-keep,allowobfuscation class androidx.compose.runtime.ComposablesKt {
  public static long getCurrentCompositeKeyHashCode(androidx.compose.runtime.Composer,int);
  public static void invalidApplier();
}
-keep,allowobfuscation interface androidx.compose.runtime.Composer {
  public boolean changed(java.lang.Object);
  public void createNode(kotlin.jvm.functions.Function0);
  public void endNode();
  public androidx.compose.runtime.Applier getApplier();
  public androidx.compose.runtime.CompositionLocalMap getCurrentCompositionLocalMap();
  public boolean getInserting();
  public java.lang.Object rememberedValue();
  public boolean shouldExecute(boolean,int);
  public void skipToGroupEnd();
  public void startReusableNode();
  public void updateRememberedValue(java.lang.Object);
  public void useNode();
  androidx.compose.runtime.Composer$Companion Companion;
}
-keep,allowobfuscation class androidx.compose.runtime.Composer$Companion {
  public java.lang.Object getEmpty();
}
-keep,allowobfuscation class androidx.compose.runtime.ComposerKt {
  public static boolean isTraceInProgress();
  public static void sourceInformation(androidx.compose.runtime.Composer,java.lang.String);
  public static void sourceInformationMarkerEnd(androidx.compose.runtime.Composer);
  public static void sourceInformationMarkerStart(androidx.compose.runtime.Composer,int,java.lang.String);
  public static void traceEventEnd();
  public static void traceEventStart(int,int,int,java.lang.String);
}
-keep,allowobfuscation class androidx.compose.runtime.CompositionContext {
}
-keep,allowobfuscation class androidx.compose.runtime.CompositionLocalKt {
  public static void CompositionLocalProvider(androidx.compose.runtime.ProvidedValue,kotlin.jvm.functions.Function2,androidx.compose.runtime.Composer,int);
}
-keep,allowobfuscation interface androidx.compose.runtime.CompositionLocalMap {
}
-keep,allowobfuscation interface androidx.compose.runtime.MutableState {
  public java.lang.Object getValue();
  public void setValue(java.lang.Object);
}
-keep,allowobfuscation class androidx.compose.runtime.ProvidableCompositionLocal {
  public androidx.compose.runtime.ProvidedValue provides(java.lang.Object);
}
-keep,allowobfuscation class androidx.compose.runtime.ProvidedValue {
  int $stable;
}
-keep,allowobfuscation interface androidx.compose.runtime.SnapshotMutationPolicy {
}
-keep,allowobfuscation class androidx.compose.runtime.SnapshotStateKt {
  public static androidx.compose.runtime.MutableState mutableStateOf$default(java.lang.Object,androidx.compose.runtime.SnapshotMutationPolicy,int,java.lang.Object);
}
-keep,allowobfuscation class androidx.compose.runtime.Updater {
  public static androidx.compose.runtime.Composer constructor-impl(androidx.compose.runtime.Composer);
  public static void init-impl(androidx.compose.runtime.Composer,java.lang.Object,kotlin.jvm.functions.Function2);
  public static void reconcile-impl(androidx.compose.runtime.Composer,kotlin.jvm.functions.Function1);
  public static void set-impl(androidx.compose.runtime.Composer,java.lang.Object,kotlin.jvm.functions.Function2);
}
-keep,allowobfuscation interface androidx.compose.runtime.internal.ComposableLambda {
}
-keep,allowobfuscation class androidx.compose.runtime.internal.ComposableLambdaKt {
  public static androidx.compose.runtime.internal.ComposableLambda composableLambdaInstance(int,boolean,java.lang.Object);
  public static androidx.compose.runtime.internal.ComposableLambda rememberComposableLambda(int,boolean,java.lang.Object,androidx.compose.runtime.Composer,int);
}
-keep,allowobfuscation @interface androidx.compose.runtime.internal.StabilityInferred {
  public int parameters();
}
-keep,allowobfuscation interface androidx.compose.ui.Alignment {
  androidx.compose.ui.Alignment$Companion Companion;
}
-keep,allowobfuscation class androidx.compose.ui.Alignment$Companion {
  public androidx.compose.ui.Alignment$Horizontal getStart();
}
-keep,allowobfuscation interface androidx.compose.ui.Alignment$Horizontal {
}
-keep,allowobfuscation class androidx.compose.ui.ComposedModifierKt {
  public static androidx.compose.ui.Modifier materializeModifier(androidx.compose.runtime.Composer,androidx.compose.ui.Modifier);
}
-keep,allowobfuscation interface androidx.compose.ui.Modifier {
  androidx.compose.ui.Modifier$Companion Companion;
}
-keep,allowobfuscation class androidx.compose.ui.Modifier$Companion {
}
-keep,allowobfuscation interface androidx.compose.ui.graphics.Shape {
}
-keep,allowobfuscation interface androidx.compose.ui.layout.MeasurePolicy {
}
-keep,allowobfuscation interface androidx.compose.ui.node.ComposeUiNode {
  androidx.compose.ui.node.ComposeUiNode$Companion Companion;
}
-keep,allowobfuscation class androidx.compose.ui.node.ComposeUiNode$Companion {
  public kotlin.jvm.functions.Function1 getApplyOnDeactivatedNodeAssertion();
  public kotlin.jvm.functions.Function0 getConstructor();
  public kotlin.jvm.functions.Function2 getSetCompositeKeyHash();
  public kotlin.jvm.functions.Function2 getSetMeasurePolicy();
  public kotlin.jvm.functions.Function2 getSetModifier();
  public kotlin.jvm.functions.Function2 getSetResolvedCompositionLocals();
}
-keep,allowobfuscation class androidx.compose.ui.platform.AndroidCompositionLocals_androidKt {
  public static androidx.compose.runtime.ProvidableCompositionLocal getLocalContext();
}
-keep,allowobfuscation class androidx.compose.ui.text.TextStyle {
}
-keep,allowobfuscation class androidx.compose.ui.text.font.FontFamily {
}
-keep,allowobfuscation class androidx.compose.ui.text.font.FontStyle {
}
-keep,allowobfuscation class androidx.compose.ui.text.font.FontWeight {
}
-keep,allowobfuscation class androidx.compose.ui.text.style.TextAlign {
}
-keep,allowobfuscation class androidx.compose.ui.text.style.TextDecoration {
}
-keep,allowobfuscation class androidx.compose.ui.unit.Dp {
  public static float constructor-impl(float);
}
-keep,allowobfuscation class androidx.lifecycle.Lifecycle {
  public androidx.lifecycle.Lifecycle$State getCurrentState();
}
-keep enum androidx.lifecycle.Lifecycle$State {
  public boolean isAtLeast(androidx.lifecycle.Lifecycle$State);
  androidx.lifecycle.Lifecycle$State RESUMED;
}
-keep,allowobfuscation class com.google.zxing.Binarizer {
}
-keep,allowobfuscation class com.google.zxing.BinaryBitmap {
  public <init>(com.google.zxing.Binarizer);
}
-keep enum com.google.zxing.DecodeHintType {
  com.google.zxing.DecodeHintType TRY_HARDER;
}
-keep,allowobfuscation class com.google.zxing.LuminanceSource {
}
-keep,allowobfuscation class com.google.zxing.MultiFormatReader {
  public <init>();
  public com.google.zxing.Result decode(com.google.zxing.BinaryBitmap,java.util.Map);
}
-keep,allowobfuscation class com.google.zxing.RGBLuminanceSource {
  public <init>(int,int,int[]);
}
-keep,allowobfuscation class com.google.zxing.Result {
  public java.lang.String getText();
}
-keep,allowobfuscation class com.google.zxing.common.HybridBinarizer {
  public <init>(com.google.zxing.LuminanceSource);
}
-keep,allowobfuscation class com.jcraft.jsch.JSch {
  public <init>();
}
-keep,allowobfuscation class com.jcraft.jsch.KeyPair {
  public void dispose();
  public static com.jcraft.jsch.KeyPair genKeyPair(com.jcraft.jsch.JSch,int,int);
  public void writePrivateKey(java.io.OutputStream);
  public void writePublicKey(java.io.OutputStream,java.lang.String);
}
-keep,allowobfuscation class com.shoesproxy.ShoesNative {
  public static int init(java.lang.String);
  public static boolean isRunning();
  public static long start(java.lang.String,com.shoesproxy.ShoesNative$SocketProtection);
  public static void stop(long);
}
-keep,allowobfuscation interface com.shoesproxy.ShoesNative$SocketProtection {
  public boolean protect(int);
}
-keep,allowobfuscation class dev.nexus.localclient.ChangedServer {
  public <init>();
}
-keep,allowobfuscation class dev.nexus.localclient.CompatEngine {
  public java.util.Set getTypes();
  public org.json.JSONObject outbound(org.json.JSONObject);
  public java.lang.String problem(org.json.JSONObject);
  public org.json.JSONObject resolved$app_release(org.json.JSONObject,android.net.Network);
  dev.nexus.localclient.CompatEngine INSTANCE;
}
-keep,allowobfuscation class dev.nexus.localclient.ComponentsKt {
  public static void PrimaryAction(kotlin.jvm.functions.Function0,androidx.compose.ui.Modifier,boolean,kotlin.jvm.functions.Function3,androidx.compose.runtime.Composer,int,int);
  public static void SecondaryAction(kotlin.jvm.functions.Function0,androidx.compose.ui.Modifier,boolean,kotlin.jvm.functions.Function3,androidx.compose.runtime.Composer,int,int);
}
-keep,allowobfuscation class dev.nexus.localclient.ConnectionUiKt {
  public static kotlin.Pair trafficBytes(android.content.Context);
}
-keep,allowobfuscation class dev.nexus.localclient.CrashReports {
  public java.lang.String text(java.lang.Throwable);
  dev.nexus.localclient.CrashReports INSTANCE;
}
-keep enum dev.nexus.localclient.GroupPlace {
}
-keep,allowobfuscation class dev.nexus.localclient.Identity {
  public java.lang.String owner(byte[]);
  public byte[] publicKey();
  public byte[] rotate();
  public byte[] sign(byte[],java.lang.String);
  dev.nexus.localclient.Identity INSTANCE;
}
-keep,allowobfuscation class dev.nexus.localclient.Invitation {
  public dev.nexus.localclient.Profile claim(java.lang.String);
  dev.nexus.localclient.Invitation$Companion Companion;
}
-keep,allowobfuscation class dev.nexus.localclient.Invitation$Companion {
  public dev.nexus.localclient.Invitation parse(java.lang.String);
}
-keep,allowobfuscation class dev.nexus.localclient.InvitationArtKt {
  public static kotlin.Pair invitationMatrix(java.lang.String);
}
-keep,allowobfuscation class dev.nexus.localclient.MainActivity {
}
-keep,allowobfuscation class dev.nexus.localclient.NetworkScreenKt {
  public static dev.nexus.localclient.DiagnosticResponse diagnosticResponse(java.lang.String,int,java.lang.String,long);
}
-keep,allowobfuscation class dev.nexus.localclient.DiagnosticResponse {
  public java.lang.String getIp();
  public java.lang.String getMessage();
}
-keep,allowobfuscation class dev.nexus.localclient.NodeAddressKt {
  public static boolean allowedNodeAddress(java.net.InetAddress);
}
-keep,allowobfuscation class dev.nexus.localclient.NodeManager {
  public <init>(android.content.Context,java.lang.String,int,java.lang.String,java.lang.String,java.lang.String,byte[],kotlin.jvm.functions.Function0,int,kotlin.jvm.internal.DefaultConstructorMarker);
  public static dev.nexus.localclient.Profile deploy$default(dev.nexus.localclient.NodeManager,java.lang.String,java.lang.String,int,int,org.json.JSONObject,int,java.lang.Object);
  public dev.nexus.localclient.Profile deploy(java.lang.String,java.lang.String,int,int,org.json.JSONObject);
  public void exportMigration(char[],java.io.OutputStream);
  public static java.lang.String owner$default(dev.nexus.localclient.NodeManager,java.lang.String,org.json.JSONObject,int,java.lang.Object);
  public java.lang.String owner(java.lang.String,org.json.JSONObject);
  public java.lang.String service(boolean);
}
-keep,allowobfuscation class dev.nexus.localclient.NodeManagerKt {
  public static org.json.JSONObject readMigration(java.io.InputStream,char[]);
  public static void writeMigration(char[],java.io.OutputStream,kotlin.jvm.functions.Function0);
}
-keep,allowobfuscation class dev.nexus.localclient.NodeProbe {
  public static java.lang.Object run$default(dev.nexus.localclient.NodeProbe,android.content.Context,java.util.List,java.util.List,javax.net.ssl.SSLSocketFactory,kotlin.jvm.functions.Function1,kotlin.jvm.functions.Function2,kotlin.coroutines.Continuation,int,java.lang.Object);
  public java.lang.Object run(android.content.Context,java.util.List,java.util.List,javax.net.ssl.SSLSocketFactory,kotlin.jvm.functions.Function1,kotlin.jvm.functions.Function2,kotlin.coroutines.Continuation);
  dev.nexus.localclient.NodeProbe INSTANCE;
}
-keep,allowobfuscation class dev.nexus.localclient.NodeQuality {
  public <init>(android.content.Context,android.net.Network);
  public java.lang.String measure(android.net.Network,java.lang.String,kotlin.jvm.functions.Function0);
}
-keep,allowobfuscation class dev.nexus.localclient.NodeRow {
  public <init>(dev.nexus.localclient.Subscription,int,org.json.JSONObject);
  public java.lang.String getFavorite();
  public int getIndex();
  public org.json.JSONObject getNode();
  public java.lang.String getType();
}
-keep,allowobfuscation class dev.nexus.localclient.OwnerAccess {
  public <init>(android.content.Context);
  public java.lang.String checkHost(java.lang.String);
  public dev.nexus.localclient.Profile enroll(java.lang.String,java.lang.String,java.lang.String,boolean,java.lang.String,int,int);
  public void forget(java.lang.String);
  public java.lang.String getCurrent();
  public java.util.List getHosts();
  public java.lang.String knownPin(java.lang.String);
  public dev.nexus.localclient.NodeManager manager(java.lang.String);
}
-keep,allowobfuscation class dev.nexus.localclient.OwnerAccessKt {
  public static java.lang.String friendlyError(java.lang.Exception);
  public static org.json.JSONObject invitationLimits$default(int,int,java.lang.Long,int,java.lang.Object);
  public static dev.nexus.localclient.Invitation sharedInvitation(java.lang.String);
}
-keep,allowobfuscation class dev.nexus.localclient.ProbeResult {
  public <init>(long,java.lang.String,java.lang.String,long);
  public java.lang.String getDetail();
  public boolean getOk();
  public java.lang.String getReason();
  int $stable;
}
-keep,allowobfuscation class dev.nexus.localclient.ProbeSite {
  public <init>(java.lang.String,java.lang.String,int);
}
-keep,allowobfuscation class dev.nexus.localclient.Profile {
  public static dev.nexus.localclient.Profile copy$default(dev.nexus.localclient.Profile,java.lang.String,java.lang.String,java.lang.String,java.lang.String,byte[],byte[],java.lang.String,java.lang.String,int,java.lang.Object);
  public byte[] getCa();
  public byte[] getDevice();
  public java.lang.String getDns();
  public java.lang.String getFingerprint();
  public java.lang.String getServer();
  public java.lang.String getSigningAlias();
  public java.lang.String getTarget();
  public java.lang.String getTlsName();
  public java.lang.String getVpnHost();
  public void save(android.content.Context);
  dev.nexus.localclient.Profile$Companion Companion;
}
-keep,allowobfuscation class dev.nexus.localclient.Profile$Companion {
  public dev.nexus.localclient.Profile load(android.content.Context);
  public dev.nexus.localclient.Profile parse(java.lang.String);
}
-keep,allowobfuscation class dev.nexus.localclient.QuickAccessKt {
  public static void afterTileUnlock(boolean,kotlin.jvm.functions.Function1,kotlin.jvm.functions.Function0);
  public static java.lang.String subscriptionLink(java.lang.String);
}
-keep,allowobfuscation class dev.nexus.localclient.ReleaseUpdates {
  public java.lang.Object check(android.content.Context,boolean,kotlin.jvm.functions.Function1,kotlin.coroutines.Continuation);
  public kotlinx.coroutines.flow.MutableStateFlow getAvailable();
  dev.nexus.localclient.ReleaseUpdates INSTANCE;
}
-keep,allowobfuscation class dev.nexus.localclient.ShoesEngine {
  public java.util.Set getTypes();
  dev.nexus.localclient.ShoesEngine INSTANCE;
}
-keep,allowobfuscation class dev.nexus.localclient.SimpleDesignKt {
  public static void ConnectionHome(dev.nexus.localclient.TunnelState,boolean,boolean,java.lang.String,boolean,java.lang.String,kotlin.jvm.functions.Function0,kotlin.jvm.functions.Function0,kotlin.jvm.functions.Function1,kotlin.jvm.functions.Function0,kotlin.jvm.functions.Function0,kotlin.jvm.functions.Function0,java.lang.String,java.lang.String,java.lang.Long,kotlin.jvm.functions.Function2,androidx.compose.runtime.Composer,int,int,int);
  public static void SimpleShell(java.lang.String,kotlin.jvm.functions.Function1,kotlin.jvm.functions.Function0,kotlin.jvm.functions.Function3,androidx.compose.runtime.Composer,int);
}
-keep,allowobfuscation class dev.nexus.localclient.SimpleServerKt {
  public static void DeviceCard(org.json.JSONObject,boolean,dev.nexus.localclient.GroupPlace,kotlin.jvm.functions.Function0,androidx.compose.runtime.Composer,int,int);
  public static void ServerSummary(java.lang.String,org.json.JSONArray,kotlin.jvm.functions.Function0,androidx.compose.runtime.Composer,int);
  public static void ShareInvite(java.lang.String,java.util.List,kotlin.jvm.functions.Function0,androidx.compose.runtime.Composer,int,int);
  public static java.lang.String usageAmount(long);
}
-keep,allowobfuscation class dev.nexus.localclient.Subscription {
  public <init>(java.lang.String,java.lang.String,java.lang.String,org.json.JSONArray,java.lang.String,long);
  public dev.nexus.localclient.Usage getAllowance$app_release();
  public java.lang.String getId();
  public java.lang.String getLabel();
  public org.json.JSONArray getNodes();
  public long getUpdated();
  public java.lang.String getUrl();
  public org.json.JSONObject json();
  int $stable;
}
-keep,allowobfuscation class dev.nexus.localclient.SubscriptionOverviewKt {
  public static void SubscriptionOverview(java.util.List,java.util.List,java.util.Map,dev.nexus.localclient.Subscriptions,java.util.Map,long,kotlin.jvm.functions.Function0,kotlin.jvm.functions.Function1,androidx.compose.runtime.Composer,int,int);
  public static java.lang.String traffic(dev.nexus.localclient.Usage);
}
-keep,allowobfuscation class dev.nexus.localclient.SubscriptionRefresh {
  public static boolean refresh$default(dev.nexus.localclient.SubscriptionRefresh,dev.nexus.localclient.Subscriptions,dev.nexus.localclient.Subscription,boolean,long,int,java.lang.Object);
  public void updateDue(dev.nexus.localclient.Subscriptions,java.util.List,long);
  dev.nexus.localclient.SubscriptionRefresh INSTANCE;
}
-keep,allowobfuscation class dev.nexus.localclient.SubscriptionScreenKt {
  public static void SubscriptionScreen(boolean,boolean,kotlin.jvm.functions.Function2,boolean,java.lang.String,kotlin.jvm.functions.Function0,kotlin.jvm.functions.Function1,kotlin.jvm.functions.Function1,kotlin.jvm.functions.Function1,kotlin.jvm.functions.Function0,kotlin.jvm.functions.Function2,kotlin.jvm.functions.Function3,androidx.compose.runtime.Composer,int,int,int);
  public static java.util.List sourceRows(java.util.List);
  public static android.content.Intent subscriptionRequest(android.content.Context,dev.nexus.localclient.NodeRow,java.util.List,java.lang.String,boolean);
}
-keep,allowobfuscation class dev.nexus.localclient.Subscriptions {
  public kotlin.Pair fetch$app_release(java.net.URL,long);
  public <init>(android.content.Context,kotlin.jvm.functions.Function1);
  public <init>(android.content.Context,kotlin.jvm.functions.Function1,int,kotlin.jvm.internal.DefaultConstructorMarker);
  public java.util.List all();
  public dev.nexus.localclient.Subscription get(java.lang.String);
  public android.content.SharedPreferences getAppearance$app_release();
  public static dev.nexus.localclient.Subscription import$default(dev.nexus.localclient.Subscriptions,java.lang.String,java.lang.String,dev.nexus.localclient.Subscription,int,java.lang.Object);
  public dev.nexus.localclient.Subscription import(java.lang.String,java.lang.String,dev.nexus.localclient.Subscription);
  public void remove(java.lang.String);
  int $stable;
}
-keep,allowobfuscation class dev.nexus.localclient.SubscriptionsKt {
  public static byte[] bounded(java.io.InputStream,int);
  public static java.lang.String nodeKey(java.lang.String,org.json.JSONObject);
}
-keep,allowobfuscation class dev.nexus.localclient.ThemeKt {
  public static void NexusTheme(kotlin.jvm.functions.Function2,androidx.compose.runtime.Composer,int);
}
-keep,allowobfuscation class dev.nexus.localclient.Tunnel {
  public <init>(java.lang.String,kotlin.jvm.functions.Function1,kotlin.jvm.functions.Function1);
  public static int run$default(dev.nexus.localclient.Tunnel,int,int,long,java.lang.String,java.lang.String,java.lang.String,java.lang.String,java.lang.String,byte[],byte[],byte[],int,java.lang.Object);
}
-keep,allowobfuscation class dev.nexus.localclient.TunnelService {
  dev.nexus.localclient.TunnelService$Companion Companion;
}
-keep,allowobfuscation class dev.nexus.localclient.TunnelService$Companion {
  public kotlinx.coroutines.flow.MutableStateFlow getState();
}
-keep,allowobfuscation class dev.nexus.localclient.TunnelState {
  public <init>(boolean,int,java.lang.String,java.lang.String,boolean,boolean,dev.nexus.localclient.Profile,long,java.lang.String,android.net.Network,java.lang.String,boolean,int,kotlin.jvm.internal.DefaultConstructorMarker);
  public boolean getActive();
  public dev.nexus.localclient.Profile getActiveProfile();
  public long getConnectedAt();
  public java.lang.String getError();
  public java.lang.String getNode();
  public android.net.Network getPhysical();
  public boolean getShoes();
  public boolean getStarting();
  public java.lang.String getSubscription();
  public boolean getVpn();
  public boolean getWaiting();
  int $stable;
}
-keep,allowobfuscation class dev.nexus.localclient.Usage {
  public java.lang.Long getExpires();
  public java.lang.Long getRemaining();
  public java.lang.Long getTotal();
  public java.lang.Long getUsed();
  dev.nexus.localclient.Usage$Companion Companion;
}
-keep,allowobfuscation class dev.nexus.localclient.Usage$Companion {
  public boolean isInfo(java.lang.String);
  public dev.nexus.localclient.Usage parse(java.lang.String,org.json.JSONArray);
}
-keep,allowobfuscation @interface kotlin.Metadata {
  public java.lang.String[] d1();
  public java.lang.String[] d2();
  public int k();
  public int[] mv();
  public int xi();
}
-keep,allowobfuscation class kotlin.Pair {
  public java.lang.Object getFirst();
  public java.lang.Object getSecond();
}
-keep,allowobfuscation class kotlin.Result {
  public static java.lang.Object constructor-impl(java.lang.Object);
  public static java.lang.Throwable exceptionOrNull-impl(java.lang.Object);
  public static boolean isFailure-impl(java.lang.Object);
  kotlin.Result$Companion Companion;
}
-keep,allowobfuscation class kotlin.Result$Companion {
}
-keep,allowobfuscation class kotlin.ResultKt {
  public static java.lang.Object createFailure(java.lang.Throwable);
  public static void throwOnFailure(java.lang.Object);
}
-keep,allowobfuscation class kotlin.TuplesKt {
  public static kotlin.Pair to(java.lang.Object,java.lang.Object);
}
-keep,allowobfuscation class kotlin.Unit {
  kotlin.Unit INSTANCE;
}
-keep,allowobfuscation class kotlin.collections.ArraysKt {
}
-keep,allowobfuscation class kotlin.collections.ArraysKt___ArraysJvmKt {
  public static byte[] plus(byte[],byte[]);
}
-keep,allowobfuscation class kotlin.collections.ArraysKt___ArraysKt {
  public static java.lang.Object firstOrNull(java.lang.Object[]);
  public static int getLastIndex(byte[]);
  public static byte last(byte[]);
  public static java.util.List take(java.lang.Object[],int);
  public static java.util.List takeLast(byte[],int);
  public static java.util.List toList(byte[]);
}
-keep,allowobfuscation class kotlin.collections.CollectionsKt {
}
-keep,allowobfuscation class kotlin.collections.CollectionsKt__CollectionsJVMKt {
  public static java.util.List listOf(java.lang.Object);
}
-keep,allowobfuscation class kotlin.collections.CollectionsKt__CollectionsKt {
  public static java.util.List emptyList();
  public static java.util.List listOf(java.lang.Object[]);
  public static void throwCountOverflow();
  public static void throwIndexOverflow();
}
-keep,allowobfuscation class kotlin.collections.CollectionsKt__IterablesKt {
  public static int collectionSizeOrDefault(java.lang.Iterable,int);
}
-keep,allowobfuscation class kotlin.collections.CollectionsKt__MutableCollectionsKt {
  public static boolean addAll(java.util.Collection,java.lang.Iterable);
}
-keep,allowobfuscation class kotlin.collections.CollectionsKt___CollectionsKt {
  public static boolean contains(java.lang.Iterable,java.lang.Object);
  public static java.util.List drop(java.lang.Iterable,int);
  public static java.lang.Object first(java.util.List);
  public static java.lang.Object firstOrNull(java.util.List);
  public static java.lang.Object getOrNull(java.util.List,int);
  public static java.lang.String joinToString$default(java.lang.Iterable,java.lang.CharSequence,java.lang.CharSequence,java.lang.CharSequence,int,java.lang.CharSequence,kotlin.jvm.functions.Function1,int,java.lang.Object);
  public static java.lang.Object last(java.util.List);
  public static java.util.List plus(java.util.Collection,java.lang.Iterable);
  public static java.util.List sorted(java.lang.Iterable);
  public static byte[] toByteArray(java.util.Collection);
}
-keep,allowobfuscation class kotlin.collections.IntIterator {
  public int nextInt();
}
-keep,allowobfuscation class kotlin.collections.MapsKt {
}
-keep,allowobfuscation class kotlin.collections.MapsKt__MapsJVMKt {
  public static int mapCapacity(int);
  public static java.util.Map mapOf(kotlin.Pair);
}
-keep,allowobfuscation class kotlin.collections.MapsKt__MapsKt {
  public static java.util.LinkedHashMap linkedMapOf(kotlin.Pair[]);
  public static java.util.Map mapOf(kotlin.Pair[]);
  public static java.util.Map toMap(java.util.Map);
}
-keep,allowobfuscation class kotlin.collections.SetsKt {
}
-keep,allowobfuscation class kotlin.collections.SetsKt__SetsKt {
  public static java.util.Set setOf(java.lang.Object[]);
}
-keep,allowobfuscation class kotlin.collections.SetsKt___SetsKt {
  public static java.util.Set minus(java.util.Set,java.lang.Iterable);
}
-keep,allowobfuscation interface kotlin.coroutines.Continuation {
}
-keep,allowobfuscation interface kotlin.coroutines.CoroutineContext {
}
-keep,allowobfuscation class kotlin.coroutines.intrinsics.IntrinsicsKt {
}
-keep,allowobfuscation class kotlin.coroutines.intrinsics.IntrinsicsKt__IntrinsicsKt {
  public static java.lang.Object getCOROUTINE_SUSPENDED();
}
-keep,allowobfuscation class kotlin.coroutines.jvm.internal.Boxing {
  public static java.lang.Boolean boxBoolean(boolean);
  public static java.lang.Integer boxInt(int);
}
-keep,allowobfuscation @interface kotlin.coroutines.jvm.internal.DebugMetadata {
  public java.lang.String c();
  public java.lang.String f();
  public int[] i();
  public int[] l();
  public java.lang.String m();
  public java.lang.String[] n();
  public java.lang.String[] s();
  public int v();
}
-keep,allowobfuscation class kotlin.coroutines.jvm.internal.RestrictedSuspendLambda {
  public <init>(int,kotlin.coroutines.Continuation);
}
-keep,allowobfuscation class kotlin.coroutines.jvm.internal.SpillingKt {
  public static java.lang.Object nullOutSpilledVariable(java.lang.Object);
}
-keep,allowobfuscation interface kotlin.coroutines.jvm.internal.SuspendFunction {
}
-keep,allowobfuscation class kotlin.coroutines.jvm.internal.SuspendLambda {
  public <init>(int,kotlin.coroutines.Continuation);
}
-keep,allowobfuscation class kotlin.io.ByteStreamsKt {
  public static byte[] readBytes(java.io.InputStream);
}
-keep,allowobfuscation class kotlin.io.CloseableKt {
  public static void closeFinally(java.io.Closeable,java.lang.Throwable);
}
-keep,allowobfuscation class kotlin.io.FilesKt {
}
-keep,allowobfuscation class kotlin.io.FilesKt__FileReadWriteKt {
  public static byte[] readBytes(java.io.File);
  public static java.lang.String readText$default(java.io.File,java.nio.charset.Charset,int,java.lang.Object);
  public static void writeText$default(java.io.File,java.lang.String,java.nio.charset.Charset,int,java.lang.Object);
}
-keep,allowobfuscation interface kotlin.jvm.functions.Function0 {
  public java.lang.Object invoke();
}
-keep,allowobfuscation interface kotlin.jvm.functions.Function1 {
  public java.lang.Object invoke(java.lang.Object);
}
-keep,allowobfuscation interface kotlin.jvm.functions.Function2 {
  public java.lang.Object invoke(java.lang.Object,java.lang.Object);
}
-keep,allowobfuscation interface kotlin.jvm.functions.Function3 {
  public java.lang.Object invoke(java.lang.Object,java.lang.Object,java.lang.Object);
}
-keep,allowobfuscation class kotlin.jvm.internal.CallableReference {
  java.lang.Object receiver;
}
-keep,allowobfuscation class kotlin.jvm.internal.DefaultConstructorMarker {
}
-keep,allowobfuscation class kotlin.jvm.internal.FunctionReferenceImpl {
  public <init>(int,java.lang.Class,java.lang.String,java.lang.String,int);
  public <init>(int,java.lang.Object,java.lang.Class,java.lang.String,java.lang.String,int);
}
-keep,allowobfuscation class kotlin.jvm.internal.Intrinsics {
  public static boolean areEqual(java.lang.Object,java.lang.Object);
  public static void checkNotNull(java.lang.Object);
  public static void checkNotNull(java.lang.Object,java.lang.String);
  public static void checkNotNullExpressionValue(java.lang.Object,java.lang.String);
  public static void checkNotNullParameter(java.lang.Object,java.lang.String);
  public static void throwUninitializedPropertyAccessException(java.lang.String);
}
-keep,allowobfuscation class kotlin.jvm.internal.Intrinsics$Kotlin {
}
-keep,allowobfuscation class kotlin.jvm.internal.Ref$BooleanRef {
  public <init>();
  boolean element;
}
-keep,allowobfuscation class kotlin.jvm.internal.Ref$IntRef {
  public <init>();
  int element;
}
-keep,allowobfuscation class kotlin.jvm.internal.Ref$ObjectRef {
  public <init>();
  java.lang.Object element;
}
-keep,allowobfuscation @interface kotlin.jvm.internal.SourceDebugExtension {
  public java.lang.String[] value();
}
-keep,allowobfuscation class kotlin.ranges.IntProgression {
}
-keep,allowobfuscation class kotlin.ranges.IntRange {
  public <init>(int,int);
}
-keep,allowobfuscation class kotlin.ranges.RangesKt {
}
-keep,allowobfuscation class kotlin.ranges.RangesKt___RangesKt {
  public static int coerceAtLeast(int,int);
  public static int coerceIn(int,int,int);
  public static kotlin.ranges.IntProgression step(kotlin.ranges.IntProgression,int);
  public static kotlin.ranges.IntRange until(int,int);
}
-keep,allowobfuscation interface kotlin.sequences.Sequence {
  public java.util.Iterator iterator();
}
-keep,allowobfuscation class kotlin.sequences.SequenceScope {
  public java.lang.Object yield(java.lang.Object,kotlin.coroutines.Continuation);
  public java.lang.Object yieldAll(kotlin.sequences.Sequence,kotlin.coroutines.Continuation);
}
-keep,allowobfuscation class kotlin.sequences.SequencesKt {
}
-keep,allowobfuscation class kotlin.sequences.SequencesKt__SequenceBuilderKt {
  public static kotlin.sequences.Sequence sequence(kotlin.jvm.functions.Function2);
}
-keep,allowobfuscation class kotlin.sequences.SequencesKt__SequencesKt {
  public static kotlin.sequences.Sequence generateSequence(java.lang.Object,kotlin.jvm.functions.Function1);
}
-keep,allowobfuscation class kotlin.sequences.SequencesKt___SequencesKt {
  public static kotlin.sequences.Sequence filter(kotlin.sequences.Sequence,kotlin.jvm.functions.Function1);
  public static kotlin.sequences.Sequence map(kotlin.sequences.Sequence,kotlin.jvm.functions.Function1);
  public static java.util.List toList(kotlin.sequences.Sequence);
}
-keep,allowobfuscation class kotlin.text.Charsets {
  java.nio.charset.Charset ISO_8859_1;
  java.nio.charset.Charset UTF_8;
}
-keep,allowobfuscation interface kotlin.text.MatchResult {
  public java.lang.String getValue();
}
-keep,allowobfuscation class kotlin.text.Regex {
  public <init>(java.lang.String);
  public static kotlin.text.MatchResult find$default(kotlin.text.Regex,java.lang.CharSequence,int,int,java.lang.Object);
  public boolean matches(java.lang.CharSequence);
}
-keep,allowobfuscation class kotlin.text.StringsKt {
}
-keep,allowobfuscation class kotlin.text.StringsKt__StringsJVMKt {
  public static java.lang.String decodeToString(byte[]);
  public static java.lang.String repeat(java.lang.CharSequence,int);
  public static java.lang.String replace$default(java.lang.String,java.lang.String,java.lang.String,boolean,int,java.lang.Object);
  public static java.lang.String replaceFirst$default(java.lang.String,java.lang.String,java.lang.String,boolean,int,java.lang.Object);
  public static boolean startsWith$default(java.lang.String,java.lang.String,boolean,int,java.lang.Object);
  public static boolean startsWith(java.lang.String,java.lang.String,boolean);
}
-keep,allowobfuscation class kotlin.text.StringsKt__StringsKt {
  public static boolean contains$default(java.lang.CharSequence,java.lang.CharSequence,boolean,int,java.lang.Object);
  public static kotlin.sequences.Sequence lineSequence(java.lang.CharSequence);
  public static java.util.List split$default(java.lang.CharSequence,char[],boolean,int,int,java.lang.Object);
  public static java.lang.String substringAfter$default(java.lang.String,char,java.lang.String,int,java.lang.Object);
  public static java.lang.String substringAfterLast$default(java.lang.String,char,java.lang.String,int,java.lang.Object);
  public static java.lang.String substringAfterLast$default(java.lang.String,java.lang.String,java.lang.String,int,java.lang.Object);
  public static java.lang.String substringBefore$default(java.lang.String,char,java.lang.String,int,java.lang.Object);
  public static java.lang.String substringBeforeLast$default(java.lang.String,char,java.lang.String,int,java.lang.Object);
  public static java.lang.CharSequence trim(java.lang.CharSequence);
}
-keep,allowobfuscation class kotlin.text.StringsKt___StringsKt {
  public static java.lang.String drop(java.lang.String,int);
  public static java.lang.String take(java.lang.String,int);
}
-keep,allowobfuscation class kotlinx.coroutines.BuildersKt {
  public static kotlinx.coroutines.Job launch$default(kotlinx.coroutines.CoroutineScope,kotlin.coroutines.CoroutineContext,kotlinx.coroutines.CoroutineStart,kotlin.jvm.functions.Function2,int,java.lang.Object);
  public static java.lang.Object runBlocking$default(kotlin.coroutines.CoroutineContext,kotlin.jvm.functions.Function2,int,java.lang.Object);
}
-keep,allowobfuscation interface kotlinx.coroutines.CompletableDeferred {
  public boolean complete(java.lang.Object);
}
-keep,allowobfuscation class kotlinx.coroutines.CompletableDeferredKt {
  public static kotlinx.coroutines.CompletableDeferred CompletableDeferred$default(kotlinx.coroutines.Job,int,java.lang.Object);
}
-keep,allowobfuscation interface kotlinx.coroutines.CoroutineScope {
}
-keep enum kotlinx.coroutines.CoroutineStart {
}
-keep,allowobfuscation interface kotlinx.coroutines.Deferred {
  public java.lang.Object await(kotlin.coroutines.Continuation);
}
-keep,allowobfuscation class kotlinx.coroutines.DelayKt {
  public static java.lang.Object delay(long,kotlin.coroutines.Continuation);
}
-keep,allowobfuscation interface kotlinx.coroutines.Job {
}
-keep,allowobfuscation class kotlinx.coroutines.JobKt {
  public static java.lang.Object cancelAndJoin(kotlinx.coroutines.Job,kotlin.coroutines.Continuation);
}
-keep,allowobfuscation class kotlinx.coroutines.TimeoutKt {
  public static java.lang.Object withTimeout(long,kotlin.jvm.functions.Function2,kotlin.coroutines.Continuation);
}
-keep,allowobfuscation interface kotlinx.coroutines.flow.Flow {
}
-keep,allowobfuscation class kotlinx.coroutines.flow.FlowKt {
  public static java.lang.Object first(kotlinx.coroutines.flow.Flow,kotlin.jvm.functions.Function2,kotlin.coroutines.Continuation);
}
-keep,allowobfuscation interface kotlinx.coroutines.flow.MutableStateFlow {
  public java.lang.Object getValue();
  public void setValue(java.lang.Object);
}
-keep,allowobfuscation class libXray.LibXray {
  public static java.lang.String invoke(java.lang.String);
}
-keep,allowobfuscation @interface org.jetbrains.annotations.NotNull {
}
