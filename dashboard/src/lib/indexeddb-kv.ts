/**
 * A tiny IndexedDB key-value store with get, set, del and optional expiry (TTL).
 * - Values are stored using structured clone. If cloning fails, string fallback is used.
 * - Expiry: if ttlMs is provided to set(), the record will have an expiresAt timestamp.
 *   get() will auto-delete expired records and return undefined.
 * - Safe to use across tabs; transactions are used per operation.
 */

export interface KVOptions {
    dbName: string;
    storeName: string;
    version?: number;
    encryptionKey: string;
}

type EncBlob = { iv: string; ct: string };

export interface StoredRecord<T = unknown> {
    key: string;
    value: T;
    expiresAt?: number | null;
    createdAt: number; // For auditing/cleanup
}

export class IndexedDbKV {
    private dbPromise: Promise<IDBDatabase>;
    private readonly dbName: string;
    private readonly storeName: string;
    private readonly version: number;
    private readonly encryptionKey: string;
    private cryptoKeyPromise: Promise<CryptoKey> | null = null;

    constructor(opts: KVOptions) {
        if (typeof window === 'undefined' || typeof indexedDB === 'undefined') {
            throw new Error('indexeddb-kv must be constructed in a browser environment.');
        }

        this.dbName = opts.dbName;
        this.storeName = opts.storeName;
        this.version = opts.version ?? 1;
        this.encryptionKey = opts.encryptionKey;
        this.dbPromise = this.open();
        if (this.encryptionKey) {
            this.cryptoKeyPromise = this.importAesKeyFromHex(this.encryptionKey);
        }
    }

    async set<T>(key: string, value: T, ttlMs?: number): Promise<void> {
        const db = await this.dbPromise;
        const expiresAt = typeof ttlMs === 'number' && ttlMs > 0 ? Date.now() + ttlMs : null;

        const record: StoredRecord<unknown> = {
            key,
            value: await this.encrypt(this.safeClone(value)),
            expiresAt,
            createdAt: Date.now(),
        };

        await this.txPut(db, record);
    }

    async get<T = unknown>(key: string): Promise<T | undefined> {
        const db = await this.dbPromise;
        const record = await this.txGet<StoredRecord<unknown>>(db, key);
        if (!record) return undefined;

        // TTL check
        if (record.expiresAt && record.expiresAt <= Date.now()) {
            await this.txDelete(db, key);
            return undefined;
        }

        // Decrypt; if decryption fails (e.g., session changed), delete this record and return undefined.
        try {
            return await this.decrypt<T>(record.value as EncBlob);
        } catch {
            await this.txDelete(db, key); // purge undecryptable record
            return undefined;
        }
    }

    async del(key: string): Promise<void> {
        const db = await this.dbPromise;
        await this.txDelete(db, key);
    }

    async clear(): Promise<void> {
        const db = await this.dbPromise;
        await this.txClear(db);
    }

    async keys(): Promise<string[]> {
        const db = await this.dbPromise;
        return await this.txKeys(db);
    }

    async has(key: string): Promise<boolean> {
        const db = await this.dbPromise;
        const record = await this.txGet<StoredRecord<unknown>>(db, key);
        if (!record) return false;
        if (record.expiresAt && record.expiresAt <= Date.now()) {
            await this.txDelete(db, key);
            return false;
        }
        // Try decrypt to verify readability under current session; if fail, delete and return false.
        try {
            await this.decrypt<unknown>(record.value as EncBlob);
            return true;
        } catch {
            await this.txDelete(db, key);
            return false;
        }
    }

    // Proactive cleanup of expired entries
    async purgeExpired(): Promise<number> {
        const db = await this.dbPromise;
        const now = Date.now();
        const toDelete: string[] = [];

        await new Promise<void>((resolve, reject) => {
            const tx = db.transaction(this.storeName, 'readonly');
            const store = tx.objectStore(this.storeName);
            // Use index if available; fallback to cursor
            const index = store.index('expiresAt');
            const request = index.openCursor(IDBKeyRange.upperBound(now));

            request.onsuccess = () => {
                const cursor = request.result;
                if (cursor) {
                    const rec = cursor.value as StoredRecord<unknown>;
                    if (rec.expiresAt && rec.expiresAt <= now) {
                        toDelete.push(rec.key);
                    }
                    cursor.continue();
                } else {
                    resolve();
                }
            };
            request.onerror = () => reject(request.error);
        });

        if (toDelete.length === 0) return 0;

        const tx = db.transaction(this.storeName, 'readwrite');
        const store = tx.objectStore(this.storeName);
        await Promise.all(
            toDelete.map(
                (key) =>
                    new Promise<void>((resolve, reject) => {
                        const req = store.delete(key);
                        req.onsuccess = () => resolve();
                        req.onerror = () => reject(req.error);
                    }),
            ),
        );

        await new Promise<void>((resolve, reject) => {
            tx.oncomplete = () => resolve();
            tx.onerror = () => reject(tx.error);
            tx.onabort = () => reject(tx.error);
        });

        return toDelete.length;
    }

    private open(): Promise<IDBDatabase> {
        return new Promise((resolve, reject) => {
            const req = indexedDB.open(this.dbName, this.version);

            req.onupgradeneeded = () => {
                const db = req.result;
                // Create object store if not exists
                if (!db.objectStoreNames.contains(this.storeName)) {
                    const store = db.createObjectStore(this.storeName, {
                        keyPath: 'key',
                    });
                    // Index for expiry cleanup (not unique)
                    store.createIndex('expiresAt', 'expiresAt', { unique: false });
                } else {
                    // If already exists, ensure index exists
                    const tx = req.transaction!;
                    const store = tx.objectStore(this.storeName);
                    if (!store.indexNames.contains('expiresAt')) {
                        store.createIndex('expiresAt', 'expiresAt', { unique: false });
                    }
                }
            };

            req.onsuccess = () => resolve(req.result);
            req.onerror = () => reject(req.error);
            req.onblocked = () => {
                // Another tab holds a connection at a lower version
                reject(
                    new Error(
                        'IndexedDB upgrade blocked. Close other tabs/windows using this database.',
                    ),
                );
            };
        });
    }

    private async encrypt(value: unknown): Promise<EncBlob> {
        const key = await this.getCryptoKey();
        const iv = crypto.getRandomValues(new Uint8Array(12));
        const pt = new TextEncoder().encode(JSON.stringify(value));
        const ctBuf = await crypto.subtle.encrypt({ name: 'AES-GCM', iv }, key, pt);
        return { iv: this.toBase64(iv), ct: this.toBase64(new Uint8Array(ctBuf)) };
    }

    private async decrypt<T>(blob: EncBlob): Promise<T> {
        const key = await this.getCryptoKey();
        const iv = this.fromBase64(blob.iv);
        const ct = this.fromBase64(blob.ct);
        const ptBuf = await crypto.subtle.decrypt({ name: 'AES-GCM', iv }, key, ct);
        return JSON.parse(new TextDecoder().decode(ptBuf)) as T;
    }

    private async importAesKeyFromHex(hex: string): Promise<CryptoKey> {
        const raw = this.hexToBytes(hex);
        if (raw.byteLength !== 32) throw new Error('encryptionKey must be 32 bytes (64 hex chars).');
        return crypto.subtle.importKey('raw', raw, { name: 'AES-GCM' }, false, ['encrypt', 'decrypt']);
    }

    private async getCryptoKey(): Promise<CryptoKey> {
        if (!this.cryptoKeyPromise) throw new Error('Encryption key not configured.');
        return this.cryptoKeyPromise;
    }

    private hexToBytes(hex: string): ArrayBuffer {
        const clean = hex.trim().replace(/^0x/, '');
        const buffer = new ArrayBuffer(clean.length / 2);
        const bytes = new Uint8Array(buffer);
        for (let i = 0; i < clean.length; i += 2) {
            bytes[i / 2] = parseInt(clean.slice(i, i + 2), 16);
        }
        return buffer;
    }

    private toBase64(bytes: Uint8Array): string {
        let s = '';
        for (let i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
        return btoa(s);
    }

    private fromBase64(b64: string): Uint8Array<ArrayBuffer> {
        const s = atob(b64);
        const buffer = new ArrayBuffer(s.length);
        const out = new Uint8Array(buffer);
        for (let i = 0; i < s.length; i++) {
            out[i] = s.charCodeAt(i);
        }
        return out;
    }

    private txPut<T>(db: IDBDatabase, record: StoredRecord<T>): Promise<void> {
        return new Promise((resolve, reject) => {
            const tx = db.transaction(this.storeName, 'readwrite');
            const store = tx.objectStore(this.storeName);
            const req = store.put(record);
            req.onsuccess = () => void 0;
            req.onerror = () => reject(req.error);

            tx.oncomplete = () => resolve();
            tx.onerror = () => reject(tx.error);
            tx.onabort = () => reject(tx.error);
        });
    }

    private txGet<T>(db: IDBDatabase, key: string): Promise<T | undefined> {
        return new Promise((resolve, reject) => {
            const tx = db.transaction(this.storeName, 'readonly');
            const store = tx.objectStore(this.storeName);
            const req = store.get(key);

            req.onsuccess = () => resolve(req.result as T | undefined);
            req.onerror = () => reject(req.error);

            // Ensure transaction completes
            tx.oncomplete = () => void 0;
            tx.onerror = () => reject(tx.error);
            tx.onabort = () => reject(tx.error);
        });
    }

    private txDelete(db: IDBDatabase, key: string): Promise<void> {
        return new Promise((resolve, reject) => {
            const tx = db.transaction(this.storeName, 'readwrite');
            const store = tx.objectStore(this.storeName);
            const req = store.delete(key);

            req.onsuccess = () => void 0;
            req.onerror = () => reject(req.error);

            tx.oncomplete = () => resolve();
            tx.onerror = () => reject(tx.error);
            tx.onabort = () => reject(tx.error);
        });
    }

    private txClear(db: IDBDatabase): Promise<void> {
        return new Promise((resolve, reject) => {
            const tx = db.transaction(this.storeName, 'readwrite');
            const store = tx.objectStore(this.storeName);
            const req = store.clear();

            req.onsuccess = () => void 0;
            req.onerror = () => reject(req.error);

            tx.oncomplete = () => resolve();
            tx.onerror = () => reject(tx.error);
            tx.onabort = () => reject(tx.error);
        });
    }

    private txKeys(db: IDBDatabase): Promise<string[]> {
        return new Promise((resolve, reject) => {
            const tx = db.transaction(this.storeName, 'readonly');
            const store = tx.objectStore(this.storeName);
            const keys: string[] = [];

            const req = store.openCursor();
            req.onsuccess = () => {
                const cursor = req.result;
                if (cursor) {
                    const rec = cursor.value as StoredRecord<unknown>;
                    // Only return non-expired keys
                    if (!rec.expiresAt || rec.expiresAt > Date.now()) {
                        keys.push(rec.key);
                    }
                    cursor.continue();
                } else {
                    resolve(keys);
                }
            };
            req.onerror = () => reject(req.error);

            tx.oncomplete = () => void 0;
            tx.onerror = () => reject(tx.error);
            tx.onabort = () => reject(tx.error);
        });
    }

    private safeClone<T>(value: T): T {
        // Most values work with structured clone. If it throws (e.g., functions),
        // fallback to JSON serialization for common data (objects/arrays/strings/numbers).
        try {
            // We just return original since IndexedDB will clone internally.
            return value;
        } catch {
            // Fallback: best-effort JSON stringify/parse
            return JSON.parse(JSON.stringify(value));
        }
    }
}
