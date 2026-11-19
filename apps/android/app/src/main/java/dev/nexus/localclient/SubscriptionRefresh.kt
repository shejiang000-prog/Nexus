package dev.nexus.localclient

import android.content.Context
import androidx.work.*
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.flow.MutableStateFlow
import java.util.concurrent.TimeUnit

internal object SubscriptionRefresh {
    var protectedSources = emptySet<String>()
    private val running = mutableSetOf<String>()
    val revision = MutableStateFlow(0L)
    private val network = Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build()

    fun testImported(context: Context,id: String) {
        val work=OneTimeWorkRequestBuilder<ImportedNodeTest>()
            .setInputData(workDataOf("id" to id)).setConstraints(network)
            .setBackoffCriteria(BackoffPolicy.EXPONENTIAL,10,TimeUnit.SECONDS).build()
        WorkManager.getInstance(context).enqueueUniqueWork("imported-node-tests",ExistingWorkPolicy.APPEND_OR_REPLACE,work)
    }

    fun schedule(context: Context) {
        // Hourly is enough for 6/12/24-hour intervals and retries; app start, resume and VPN
        // disconnect also enqueue a one-off check. UPDATE replaces the earlier 15-minute period.
        val periodic = PeriodicWorkRequestBuilder<RefreshWorker>(1, TimeUnit.HOURS)
            .setConstraints(network).build()
        WorkManager.getInstance(context).enqueueUniquePeriodicWork("subscription-periodic", ExistingPeriodicWorkPolicy.UPDATE, periodic)
        enqueue(context)
    }

    fun enqueue(context: Context) {
        val work = OneTimeWorkRequestBuilder<RefreshWorker>().setConstraints(network).build()
        WorkManager.getInstance(context).enqueueUniqueWork("subscription-due", ExistingWorkPolicy.APPEND_OR_REPLACE, work)
    }

    fun updateDue(store: Subscriptions, sources: List<Subscription> = store.all(), now: Long = System.currentTimeMillis()) {
        for (source in sources) refresh(store, source, now = now)
    }

    fun refresh(store: Subscriptions, source: Subscription, force: Boolean = false, now: Long = System.currentTimeMillis()): Boolean {
        val id = source.id
        val prefs = store.appearance
        synchronized(this) {
            if (id in running || id in protectedSources || source.url.isEmpty()) return false
            if (!force) {
                val interval = store.interval(source)
                val failures = prefs.getInt("failures/"+id, 0)
                val next = if (failures == 0) source.updated + TimeUnit.HOURS.toMillis(interval.toLong())
                    else prefs.getLong("attempt/"+id, 0) + TimeUnit.MINUTES.toMillis(15L shl (failures-1).coerceIn(0,6))
                if (interval == 0 || now < next) return false
            }
            // Another worker may have finished since this batch read its source list.
            if (runCatching { store.get(id).updated }.getOrNull() != source.updated) return false
            running.add(id)
        }
        try {
            store.import(source.label, source.url, source)
            synchronized(this) {
                if (runCatching { store.get(id) }.isSuccess)
                    prefs.edit().remove("failures/"+id).remove("error/"+id).remove("attempt/"+id).apply()
                revision.value++
            }
            return true
        } catch (e: Exception) {
            synchronized(this) {
                if (id in protectedSources || runCatching { store.get(id).updated }.getOrNull() != source.updated) return false
                val reason = when (e) {
                    is java.net.SocketTimeoutException -> "连接超时"
                    is javax.net.ssl.SSLException -> "安全连接失败"
                    is java.io.IOException -> "网络请求失败"
                    is IllegalStateException -> "来源响应异常"
                    else -> "订阅格式无效"
                }
                prefs.edit().putLong("attempt/"+id, now).putString("error/"+id,reason)
                    .putInt("failures/"+id,(prefs.getInt("failures/"+id,0)+1).coerceAtMost(7)).apply()
                revision.value++
            }
            return false
        } finally { synchronized(this) { running.remove(id) } }
    }
}

class RefreshWorker(context: Context, params: WorkerParameters): Worker(context,params) {
    override fun doWork(): Result {
        SubscriptionRefresh.updateDue(Subscriptions(applicationContext))
        return Result.success()
    }
}

class ImportedNodeTest(context: Context,params:WorkerParameters): CoroutineWorker(context,params) {
    override suspend fun doWork(): Result {
        val id=inputData.getString("id") ?: return Result.failure()
        val source=runCatching { Subscriptions(applicationContext).get(id) }.getOrNull() ?: return Result.success()
        val nodes=(0 until source.nodes.length()).map { NodeRow(source,it,source.nodes.getJSONObject(it)) }
            .filter { CompatEngine.problem(it.node)==null }
        if(nodes.isEmpty()) return Result.success()
        return try {
            NodeProbe.run(applicationContext,nodes) { _,_ -> }
            synchronized(SubscriptionRefresh) { SubscriptionRefresh.revision.value++ }
            Result.success()
        } catch(e:kotlinx.coroutines.CancellationException) {
            kotlinx.coroutines.currentCoroutineContext().ensureActive()
            Result.retry()
        } catch(_:Exception) { Result.retry() }
    }
}
