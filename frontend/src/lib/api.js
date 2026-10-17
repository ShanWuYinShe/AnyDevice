// 后端 API 封装：统一 JSON POST 与错误抛出
async function postJSON(path, body) {
  const res = await fetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    throw new Error(await res.text());
  }
  return res.json();
}

export const api = {
  fetchSelf: async () => {
    const res = await fetch('/api/self');
    if (!res.ok) throw new Error('fetch /api/self failed');
    return res.json();
  },
  fetchPeers: async () => {
    const res = await fetch('/api/peers');
    if (!res.ok) throw new Error('fetch /api/peers failed');
    return res.json();
  },
  renameSelf: (name) => postJSON('/api/self', { name }),
  updateSelfProvides: (payload) => postJSON('/api/self', payload),
  renamePeer: (peerId, name) => postJSON('/api/peers/rename', { peer_id: peerId, name }),
  addPeer: (target, name) => postJSON('/api/peers/add', { target, name }),
  updateMapping: (peerId, payload) => postJSON('/api/peers/map', { peer_id: peerId, ...payload }),
  selectNic: (ip) => postJSON('/api/interfaces/select', { ip }),
};
