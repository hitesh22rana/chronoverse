import { IndexedDbKV } from "@/lib/indexeddb-kv";

const FIFTEEN_MINUTES = 15 * 60 * 1000;

const kv = new IndexedDbKV({
    dbName: 'chronoverse',
    storeName: 'job-logs-cache',
    encryptionKey: "",
});

async function runPurge() {
    try {
        const deleted = await kv?.purgeExpired();
        self.postMessage({ type: 'purge-complete', deleted });
    } catch (err) {
        self.postMessage({ type: 'purge-error', error: String(err) });
    }
}
runPurge();

const id = setInterval(runPurge, FIFTEEN_MINUTES);

self.addEventListener('message', (e: MessageEvent) => {
    if (e.data?.type === 'purge-now') void runPurge();
});

self.addEventListener('close', () => clearInterval(id));
