# The source is public (GPL-3.0); renaming only makes crash stacks unreadable. Keep R8
# shrinking and optimisation, but not obfuscation.
-dontobfuscate
# Rust and shoes look up these binary names and callback descriptors through JNI.
-keep class dev.nexus.localclient.Tunnel {
    native <methods>;
    byte[] sign(byte[]);
    boolean protectTransport(int);
    void onReady(int);
    void onPath(long,long,long);
}
-keep class com.shoesproxy.ShoesNative { native <methods>; }
-keep interface com.shoesproxy.ShoesNative$SocketProtection { *; }
-keepclassmembers class * implements com.shoesproxy.ShoesNative$SocketProtection { boolean protect(int); }
# gomobile creates proxies in native code, including classes with no Java constructor call site.
-keep class go.** { *; }
-keep class libXray.** { *; }
-keep interface libXray.** { *; }
-keep class mobile.** { *; }
-keep class enrollment.** { *; }
-keep class v2vpn.** { *; }
# JSch's algorithm map and ZXing's embedded Activity use class names.
-keep class com.jcraft.jsch.** { public <init>(...); }
-keep class com.journeyapps.barcodescanner.CaptureActivity { public <init>(); }
# org.json comes from Android's boot classpath. Preserve JSON constructors if bundled by a provider.
-keepclassmembers class org.json.** { public <init>(...); }
-keepattributes SourceFile,LineNumberTable,InnerClasses,EnclosingMethod,Signature

# JSch probes optional desktop SSH agents, logging adapters, GSS and alternative crypto
# providers. The Android client uses password/local-key auth, platform crypto and no logger.
-dontwarn com.sun.jna.**
-dontwarn org.newsclub.net.unix.**
-dontwarn org.apache.logging.log4j.**
-dontwarn org.apache.log4j.**
-dontwarn org.ietf.jgss.**
-dontwarn org.slf4j.**
-dontwarn org.bouncycastle.**
