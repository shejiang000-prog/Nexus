package dev.nexus.localclient;

import org.junit.Test;
public final class V2ErrorsTest {
    @Test
    public void mappedErrorsExplainCauseAndRetryPolicy() {
        String[] categories = {"配置", "证书", "认证", "资源", "目标", "中断", "已经送达"};
        for (int code = 1; code <= categories.length; code++) {
            String message = V2Connection.INSTANCE.error(code);
            if (!message.contains(categories[code - 1])
                    || V2Connection.INSTANCE.retry(code) != (code == 4 || code == 6)) {
                throw new AssertionError("Wrong message or retry policy for error " + code);
            }
        }
        String auth = V2Connection.INSTANCE.error(3);
        if (!auth.contains("撤销") || !auth.contains("到期")) {
            throw new AssertionError("Authentication failure must explain revocation and expiry");
        }
    }
}
