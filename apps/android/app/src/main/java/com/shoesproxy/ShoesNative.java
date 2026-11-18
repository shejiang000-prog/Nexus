package com.shoesproxy;

/** JNI names are the embedding ABI of upstream shoes. */
public final class ShoesNative {
    static { System.loadLibrary("shoes"); }
    private ShoesNative() {}
    public interface SocketProtection { boolean protect(int fd); }
    public static native int init(String logLevel);
    public static native long start(String config, SocketProtection protection);
    public static native boolean isRunning();
    public static native void stop(long handle);
}
