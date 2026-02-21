package auth

const loginScript = `
function base64urlToBuffer(base64url) {
    const base64 = base64url.replace(/-/g, '+').replace(/_/g, '/');
    const pad = base64.length % 4;
    const padded = pad ? base64 + '='.repeat(4 - pad) : base64;
    const binary = atob(padded);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
    return bytes.buffer;
}
function bufferToBase64url(buffer) {
    const bytes = new Uint8Array(buffer);
    let binary = '';
    for (const b of bytes) binary += String.fromCharCode(b);
    return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '');
}
async function doLogin() {
    const btn = document.getElementById('login-btn');
    const errDiv = document.getElementById('error');
    btn.disabled = true;
    errDiv.style.display = 'none';
    try {
        const beginResp = await fetch('/auth/login/begin', {method: 'POST'});
        if (!beginResp.ok) throw new Error('Login failed');
        const challengeID = beginResp.headers.get('X-Challenge-ID');
        const options = await beginResp.json();
        options.publicKey.challenge = base64urlToBuffer(options.publicKey.challenge);
        for (const cred of options.publicKey.allowCredentials || []) {
            cred.id = base64urlToBuffer(cred.id);
        }
        const assertion = await navigator.credentials.get({publicKey: options.publicKey});
        const finishResp = await fetch('/auth/login/finish', {
            method: 'POST',
            headers: {'Content-Type': 'application/json', 'X-Challenge-ID': challengeID},
            body: JSON.stringify({
                id: assertion.id,
                rawId: bufferToBase64url(assertion.rawId),
                type: assertion.type,
                response: {
                    authenticatorData: bufferToBase64url(assertion.response.authenticatorData),
                    clientDataJSON: bufferToBase64url(assertion.response.clientDataJSON),
                    signature: bufferToBase64url(assertion.response.signature),
                    userHandle: assertion.response.userHandle ? bufferToBase64url(assertion.response.userHandle) : '',
                },
            }),
        });
        if (!finishResp.ok) throw new Error('Login verification failed');
        window.location.href = '/';
    } catch (e) {
        errDiv.textContent = e.message || 'Login failed';
        errDiv.style.display = 'block';
        btn.disabled = false;
    }
}
`

const registerScript = `
function base64urlToBuffer(base64url) {
    const base64 = base64url.replace(/-/g, '+').replace(/_/g, '/');
    const pad = base64.length % 4;
    const padded = pad ? base64 + '='.repeat(4 - pad) : base64;
    const binary = atob(padded);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
    return bytes.buffer;
}
function bufferToBase64url(buffer) {
    const bytes = new Uint8Array(buffer);
    let binary = '';
    for (const b of bytes) binary += String.fromCharCode(b);
    return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=/g, '');
}
async function doRegister() {
    const btn = document.getElementById('register-btn');
    const errDiv = document.getElementById('error');
    btn.disabled = true;
    errDiv.style.display = 'none';
    try {
        const beginResp = await fetch('/auth/register/begin', {method: 'POST'});
        if (!beginResp.ok) throw new Error('Registration failed');
        const challengeID = beginResp.headers.get('X-Challenge-ID');
        const options = await beginResp.json();
        options.publicKey.challenge = base64urlToBuffer(options.publicKey.challenge);
        options.publicKey.user.id = base64urlToBuffer(options.publicKey.user.id);
        if (options.publicKey.excludeCredentials) {
            for (const cred of options.publicKey.excludeCredentials) {
                cred.id = base64urlToBuffer(cred.id);
            }
        }
        const credential = await navigator.credentials.create({publicKey: options.publicKey});
        const finishResp = await fetch('/auth/register/finish', {
            method: 'POST',
            headers: {'Content-Type': 'application/json', 'X-Challenge-ID': challengeID},
            body: JSON.stringify({
                id: credential.id,
                rawId: bufferToBase64url(credential.rawId),
                type: credential.type,
                response: {
                    attestationObject: bufferToBase64url(credential.response.attestationObject),
                    clientDataJSON: bufferToBase64url(credential.response.clientDataJSON),
                },
            }),
        });
        if (!finishResp.ok) throw new Error('Registration verification failed');
        window.location.href = '/auth/login';
    } catch (e) {
        errDiv.textContent = e.message || 'Registration failed';
        errDiv.style.display = 'block';
        btn.disabled = false;
    }
}
`
