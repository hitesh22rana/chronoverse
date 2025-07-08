class TrieNode {
    children: Map<string, TrieNode>;
    // Each element in 'occurrences' marks the start of a suffix
    // that begins at this node.
    // It stores the original line index and the start index of this suffix within that line.
    occurrences: Array<{ lineIndex: number; originalStartIndex: number }>;

    constructor() {
        this.children = new Map();
        this.occurrences = [];
    }
}

export class Trie {
    root: TrieNode;

    constructor() {
        this.root = new TrieNode();
    }

    // Inserts all suffixes of a line into the Trie
    insert(line: string, lineIndex: number): void {
        const lowerLine = line.toLowerCase(); // Normalize to lowercase
        for (let i = 0; i < lowerLine.length; i++) {
            let node = this.root;
            // For each suffix starting at index i
            for (let j = i; j < lowerLine.length; j++) {
                const char = lowerLine[j];
                if (!node.children.has(char)) {
                    node.children.set(char, new TrieNode());
                }
                node = node.children.get(char)!;
            }
            // Mark the end of this suffix (which is also a node in the Trie path)
            // and store its original line index and starting position in the original line.
            node.occurrences.push({ lineIndex, originalStartIndex: i });
        }
    }

    // Searches for a query string (substring) in the Trie
    search(query: string): Array<{ lineIndex: number; startIndex: number; endIndex: number }> {
        const results: Array<{ lineIndex: number; startIndex: number; endIndex: number }> = [];
        if (!query) {
            return results;
        }
        const lowerQuery = query.toLowerCase(); // Normalize query to lowercase

        let node = this.root;
        // Traverse the Trie for the query string
        for (const char of lowerQuery) {
            if (!node.children.has(char)) {
                return results; // Query string not found as a path in the Trie
            }
            node = node.children.get(char)!;
        }

        // If we reached this point, the 'node' represents the Trie node
        // corresponding to the end of the query string.
        // All suffixes that start with this query string will have passed through this node.
        // We need to collect all occurrences from this node downwards,
        // as any path from this node represents a string that starts with the query.

        this._collectMatches(node, lowerQuery.length, results);

        return results;
    }

    // Helper function to collect all occurrences from a given node and its descendants.
    // All these occurrences started with the query string.
    private _collectMatches(
        node: TrieNode,
        queryLength: number,
        results: Array<{ lineIndex: number; startIndex: number; endIndex: number }>
    ): void {
        // Add all occurrences stored at the current node
        // These are suffixes that exactly match the query (or query is a prefix of them,
        // and this node is where that prefix path ends)
        for (const occurrence of node.occurrences) {
            results.push({
                lineIndex: occurrence.lineIndex,
                startIndex: occurrence.originalStartIndex, // This is the start of the query in the original line
                endIndex: occurrence.originalStartIndex + queryLength,
            });
        }

        // Recursively visit all children to find suffixes that *contain* the query as a prefix
        for (const childNode of node.children.values()) {
            this._collectMatches(childNode, queryLength, results);
        }
    }
}
