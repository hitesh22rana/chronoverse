"use client"

import React, { useEffect, useMemo, useState } from 'react';

import { IndexedDbKV } from '@/lib/indexeddb-kv';
import { getSessionHash } from '@/actions';

export const KVContext = React.createContext<{ kv: IndexedDbKV | null }>({ kv: null });

export function KVProvider({ children }: { children: React.ReactNode }) {
    const [encryptionKey, setEncryptionKey] = useState<string | undefined>(undefined);

    // Fetch session hash on mount, safely cancel on unmount
    useEffect(() => {
        const ac = new AbortController();

        (async () => {
            try {
                const key = await getSessionHash();
                if (!ac.signal.aborted) {
                    setEncryptionKey(key ?? undefined);
                }
            } catch {
                if (!ac.signal.aborted) {
                    setEncryptionKey(undefined);
                }
            }
        })();

        return () => {
            ac.abort();
        };
    }, []);

    // Instantiate KV when encryptionKey is ready
    const kv = useMemo(() => {
        if (!encryptionKey) return null;
        try {
            return new IndexedDbKV({
                dbName: 'chronoverse',
                storeName: 'job-logs-cache',
                encryptionKey,
            });
        } catch {
            return null;
        }
    }, [encryptionKey]);

    // Start purge worker only when KV is ready
    useEffect(() => {
        if (!kv) return;
        const worker = new Worker(new URL('../workers/kvpurge.worker.ts', import.meta.url));
        worker.onmessage = () => { };
        return () => worker.terminate();
    }, [kv]);

    return (
        <KVContext.Provider value={{ kv }}>
            {children}
        </KVContext.Provider>
    );
}
