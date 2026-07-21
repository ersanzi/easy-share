// EasyShare Shell Extension — branded name + subtitle in "此电脑" tile view.
// Name comes from registry Default value; subtitle comes from System.ItemTypeText
// property (requested by TileInfo=prop:System.ItemTypeText in NameSpace registry).
//
// Build:
//   g++ -shared -o EasyShareExt.dll EasyShareExt.cpp EasyShareExt.def \
//       -lole32 -loleaut32 -luuid -lshell32 -lshlwapi \
//       -static-libgcc -static-libstdc++ -Wl,--kill-at \
//       -DUNICODE -D_UNICODE -D_WIN32_WINNT=0x0601
#include <windows.h>
#include <shlobj.h>
#include <shlwapi.h>
#include <shobjidl.h>
#include <propsys.h>
#include <propidl.h>
#include <string.h>

// System.ItemName — display name in tile view.
// FMTID {B725F130-47EF-101A-A5F1-02608C9EEBAC}, pid 10
static const PROPERTYKEY PKEY_ItemName_Val = {
    {0xB725F130, 0x47EF, 0x101A, {0xA5, 0xF1, 0x02, 0x60, 0x8C, 0x9E, 0xEB, 0xAC}}, 10
};
// System.ItemTypeText — tile subtitle (requested by TileInfo=prop:System.ItemTypeText).
// FMTID {28636AA6-953D-11D2-B5D6-00C04FD918D0}, pid 11
static const PROPERTYKEY PKEY_ItemTypeText_Val = {
    {0x28636AA6, 0x953D, 0x11D2, {0xB5, 0xD6, 0x00, 0xC0, 0x4F, 0xD9, 0x18, 0xD0}}, 11
};

static const CLSID CLSID_ES_Cloud = {0xE5A1F2B3,0xC4D5,0x6E7F,{0x8A,0x9B,0x0C,0x1D,0x2E,0x3F,0x4A,0x5B}};
static const CLSID CLSID_ES_Share = {0xF6B2A3C4,0xD5E6,0x7F8A,{0x9B,0x0C,0x1D,0x2E,0x3F,0x4A,0x5B,0x6C}};

static const wchar_t* NAME_CLOUD     = L"EasyShare \x7f51\x76d8";           // EasyShare 网盘
static const wchar_t* NAME_SHARE     = L"EasyShare \x5171\x4eab";           // EasyShare 共享
static const wchar_t* SUBTITLE_CLOUD = L"\x53cc\x51fb\x8fdb\x5165 EasyShare \x7f51\x76d8"; // 双击进入 EasyShare 网盘
static const wchar_t* SUBTITLE_SHARE = L"\x53cc\x51fb\x8fdb\x5165\x5c40\x57df\x7f51\x5171\x4eab"; // 双击进入局域网共享

static LONG g_refCount = 0;
static HINSTANCE g_hInst = NULL;

static const wchar_t* GetNameForCLSID(REFCLSID clsid) {
    if (IsEqualCLSID(clsid, CLSID_ES_Cloud)) return NAME_CLOUD;
    if (IsEqualCLSID(clsid, CLSID_ES_Share)) return NAME_SHARE;
    return L"EasyShare";
}
static const wchar_t* GetSubtitleForCLSID(REFCLSID clsid) {
    if (IsEqualCLSID(clsid, CLSID_ES_Cloud)) return SUBTITLE_CLOUD;
    if (IsEqualCLSID(clsid, CLSID_ES_Share)) return SUBTITLE_SHARE;
    return L"EasyShare";
}

// ─── ESPropertyStore: provides ItemName + ItemTypeText for tile view ───

class ESPropertyStore : public IPropertyStore {
public:
    ESPropertyStore(const wchar_t* name, const wchar_t* subtitle) : m_ref(1) {
        m_name = _wcsdup(name ? name : L"");
        m_subtitle = _wcsdup(subtitle ? subtitle : L"");
        InterlockedIncrement(&g_refCount);
    }
    ~ESPropertyStore() {
        free(m_name);
        free(m_subtitle);
        InterlockedDecrement(&g_refCount);
    }

    STDMETHODIMP QueryInterface(REFIID riid, void** ppv) {
        if (!ppv) return E_POINTER;
        if (IsEqualIID(riid, IID_IUnknown) || IsEqualIID(riid, IID_IPropertyStore)) {
            *ppv = static_cast<IPropertyStore*>(this);
            AddRef(); return S_OK;
        }
        *ppv = NULL; return E_NOINTERFACE;
    }
    STDMETHODIMP_(ULONG) AddRef()  { return InterlockedIncrement(&m_ref); }
    STDMETHODIMP_(ULONG) Release() { LONG r = InterlockedDecrement(&m_ref); if (r == 0) delete this; return r; }

    STDMETHODIMP GetCount(DWORD* cProps) {
        if (!cProps) return E_POINTER;
        *cProps = 2;
        return S_OK;
    }
    STDMETHODIMP GetAt(DWORD iProp, PROPERTYKEY* pkey) {
        if (!pkey) return E_POINTER;
        if (iProp == 0) { *pkey = PKEY_ItemName_Val; return S_OK; }
        if (iProp == 1) { *pkey = PKEY_ItemTypeText_Val; return S_OK; }
        return E_INVALIDARG;
    }
    STDMETHODIMP GetValue(REFPROPERTYKEY key, PROPVARIANT* pv) {
        if (!pv) return E_POINTER;
        PropVariantInit(pv);
        const wchar_t* val = NULL;
        if (IsEqualPropertyKey(key, PKEY_ItemName_Val)) val = m_name;
        else if (IsEqualPropertyKey(key, PKEY_ItemTypeText_Val)) val = m_subtitle;
        if (val) {
            pv->vt = VT_LPWSTR;
            pv->pwszVal = (LPWSTR)CoTaskMemAlloc((wcslen(val) + 1) * sizeof(WCHAR));
            if (pv->pwszVal) wcscpy(pv->pwszVal, val);
            return S_OK;
        }
        return E_NOTIMPL;
    }
    STDMETHODIMP SetValue(REFPROPERTYKEY, REFPROPVARIANT) { return E_NOTIMPL; }
    STDMETHODIMP Commit() { return S_OK; }

private:
    LONG m_ref;
    wchar_t* m_name;
    wchar_t* m_subtitle;
};

// ─── EasyShareFolder: IShellFolder2 + IPersistFolder2 + IPropertyStoreFactory ───

class EasyShareFolder : public IShellFolder2, public IPersistFolder2, public IPropertyStoreFactory {
public:
    EasyShareFolder(REFCLSID clsid)
        : m_ref(1), m_inner(NULL), m_pidl(NULL), m_clsid(clsid),
          m_name(GetNameForCLSID(clsid)), m_subtitle(GetSubtitleForCLSID(clsid)) {
        InterlockedIncrement(&g_refCount);
    }
    ~EasyShareFolder() {
        if (m_inner) m_inner->Release();
        if (m_pidl) CoTaskMemFree(m_pidl);
        InterlockedDecrement(&g_refCount);
    }

    // IUnknown
    STDMETHODIMP QueryInterface(REFIID riid, void** ppv) {
        if (!ppv) return E_POINTER;
        if (IsEqualIID(riid, IID_IUnknown) || IsEqualIID(riid, IID_IShellFolder) || IsEqualIID(riid, IID_IShellFolder2))
            *ppv = static_cast<IShellFolder2*>(this);
        else if (IsEqualIID(riid, IID_IPersist) || IsEqualIID(riid, IID_IPersistFolder) || IsEqualIID(riid, IID_IPersistFolder2))
            *ppv = static_cast<IPersistFolder2*>(this);
        else if (IsEqualIID(riid, IID_IPropertyStoreFactory))
            *ppv = static_cast<IPropertyStoreFactory*>(this);
        else if (IsEqualIID(riid, IID_IPropertyStore)) {
            ESPropertyStore* ps = new ESPropertyStore(m_name, m_subtitle);
            if (!ps) return E_OUTOFMEMORY;
            HRESULT hr = ps->QueryInterface(riid, ppv);
            ps->Release();
            return hr;
        }
        else { *ppv = NULL; return E_NOINTERFACE; }
        AddRef(); return S_OK;
    }
    STDMETHODIMP_(ULONG) AddRef() { return InterlockedIncrement(&m_ref); }
    STDMETHODIMP_(ULONG) Release() { LONG r = InterlockedDecrement(&m_ref); if (r == 0) delete this; return r; }

    // IPersist / IPersistFolder / IPersistFolder2
    STDMETHODIMP GetClassID(CLSID* p) { *p = m_clsid; return S_OK; }
    STDMETHODIMP Initialize(PCIDLIST_ABSOLUTE pidl) {
        if (m_pidl) { CoTaskMemFree(m_pidl); m_pidl = NULL; }
        if (pidl) { UINT sz = ILGetSize(pidl); m_pidl = (LPITEMIDLIST)CoTaskMemAlloc(sz); if (m_pidl) memcpy(m_pidl, pidl, sz); }
        EnsureInner();
        if (m_inner) { IPersistFolder2* pf = NULL; if (SUCCEEDED(m_inner->QueryInterface(IID_IPersistFolder2, (void**)&pf))) { pf->Initialize(pidl); pf->Release(); } }
        return S_OK;
    }
    STDMETHODIMP GetCurFolder(PIDLIST_ABSOLUTE* ppidl) { if (!ppidl) return E_POINTER; if (!m_pidl) return E_FAIL; *ppidl = ILClone(m_pidl); return S_OK; }

    // IShellFolder — delegate to inner CFSFolder
    STDMETHODIMP ParseDisplayName(HWND h, IBindCtx* b, LPWSTR n, ULONG* e, PIDLIST_RELATIVE* p, ULONG* a) { return m_inner ? m_inner->ParseDisplayName(h,b,n,e,p,a) : E_FAIL; }
    STDMETHODIMP EnumObjects(HWND h, SHCONTF f, IEnumIDList** p) { return m_inner ? m_inner->EnumObjects(h,f,p) : E_FAIL; }
    STDMETHODIMP BindToObject(PCUIDLIST_RELATIVE p, IBindCtx* b, REFIID r, void** v) { return m_inner ? m_inner->BindToObject(p,b,r,v) : E_FAIL; }
    STDMETHODIMP BindToStorage(PCUIDLIST_RELATIVE p, IBindCtx* b, REFIID r, void** v) { return m_inner ? m_inner->BindToStorage(p,b,r,v) : E_FAIL; }
    STDMETHODIMP CompareIDs(LPARAM l, PCUIDLIST_RELATIVE a, PCUIDLIST_RELATIVE b) { return m_inner ? m_inner->CompareIDs(l,a,b) : E_FAIL; }
    STDMETHODIMP CreateViewObject(HWND h, REFIID r, void** v) { return m_inner ? m_inner->CreateViewObject(h,r,v) : E_FAIL; }
    STDMETHODIMP GetAttributesOf(UINT c, PCUITEMID_CHILD_ARRAY a, SFGAOF* f) { return m_inner ? m_inner->GetAttributesOf(c,a,f) : E_FAIL; }
    STDMETHODIMP GetUIObjectOf(HWND h, UINT c, PCUITEMID_CHILD_ARRAY a, REFIID r, UINT* g, void** v) { return m_inner ? m_inner->GetUIObjectOf(h,c,a,r,g,v) : E_FAIL; }
    STDMETHODIMP SetNameOf(HWND h, PCUITEMID_CHILD p, LPCWSTR n, SHGDNF f, PITEMID_CHILD* o) { return m_inner ? m_inner->SetNameOf(h,p,n,f,o) : E_FAIL; }

    // GetDisplayNameOf — branded name for the root folder.
    STDMETHODIMP GetDisplayNameOf(PCUITEMID_CHILD pidl, SHGDNF uFlags, STRRET* pName) {
        if (!m_inner) return E_FAIL;
        if (!pidl || ILIsEmpty((PUITEMID_CHILD)pidl)) {
            pName->uType = STRRET_WSTR;
            size_t len = wcslen(m_name);
            LPWSTR buf = (LPWSTR)CoTaskMemAlloc((len+1)*sizeof(WCHAR));
            if (buf) { wcscpy(buf, m_name); pName->pOleStr = buf; }
            return S_OK;
        }
        return m_inner->GetDisplayNameOf(pidl, uFlags, pName);
    }

    // IShellFolder2
    STDMETHODIMP GetDefaultSearchGUID(GUID* g) { return m_inner ? m_inner->GetDefaultSearchGUID(g) : E_FAIL; }
    STDMETHODIMP EnumSearches(IEnumExtraSearch** p) { return m_inner ? m_inner->EnumSearches(p) : E_FAIL; }
    STDMETHODIMP GetDefaultColumn(DWORD d, ULONG* s, ULONG* p) { return m_inner ? m_inner->GetDefaultColumn(d,s,p) : E_FAIL; }
    STDMETHODIMP GetDefaultColumnState(UINT i, SHCOLSTATEF* f) { return m_inner ? m_inner->GetDefaultColumnState(i,f) : E_FAIL; }
    STDMETHODIMP GetDetailsEx(PCUITEMID_CHILD p, const SHCOLUMNID* scid, VARIANT* v) { return m_inner ? m_inner->GetDetailsEx(p,scid,v) : E_FAIL; }
    STDMETHODIMP MapColumnToSCID(UINT i, SHCOLUMNID* s) { return m_inner ? m_inner->MapColumnToSCID(i,s) : E_FAIL; }

    // GetDetailsOf — column 0 = name, column 1 = subtitle
    STDMETHODIMP GetDetailsOf(PCUITEMID_CHILD pidl, UINT iColumn, SHELLDETAILS* psd) {
        if (!m_inner) return E_FAIL;
        if (!pidl) {
            HRESULT hr = m_inner->GetDetailsOf(pidl, iColumn, psd);
            if (SUCCEEDED(hr)) {
                if (iColumn == 0) SetStrRet(&psd->str, L"\x540d\x79f0"); // 名称
                if (iColumn == 1) SetStrRet(&psd->str, L"\x7c7b\x578b"); // 类型
            }
            return hr;
        }
        if (iColumn == 0) {
            psd->fmt = LVCFMT_LEFT; psd->cxChar = 24;
            SetStrRet(&psd->str, m_name);
            return S_OK;
        }
        if (iColumn == 1) {
            psd->fmt = LVCFMT_LEFT; psd->cxChar = 30;
            SetStrRet(&psd->str, m_subtitle);
            return S_OK;
        }
        return m_inner->GetDetailsOf(pidl, iColumn, psd);
    }

    // IPropertyStoreFactory — provides System.ItemTypeText for tile subtitle.
    STDMETHODIMP GetPropertyStore(GETPROPERTYSTOREFLAGS flags, IUnknown* pUnkFactory, REFIID riid, void** ppv) {
        ESPropertyStore* ps = new ESPropertyStore(m_name, m_subtitle);
        if (!ps) return E_OUTOFMEMORY;
        HRESULT hr = ps->QueryInterface(riid, ppv);
        ps->Release();
        return hr;
    }
    STDMETHODIMP GetPropertyStoreForKeys(const PROPERTYKEY* rgKeys, UINT cKeys, GETPROPERTYSTOREFLAGS flags, REFIID riid, void** ppv) {
        ESPropertyStore* ps = new ESPropertyStore(m_name, m_subtitle);
        if (!ps) return E_OUTOFMEMORY;
        HRESULT hr = ps->QueryInterface(riid, ppv);
        ps->Release();
        return hr;
    }

private:
    static void SetStrRet(STRRET* sr, const wchar_t* text) {
        sr->uType = STRRET_WSTR;
        size_t len = wcslen(text);
        LPWSTR buf = (LPWSTR)CoTaskMemAlloc((len+1)*sizeof(WCHAR));
        if (buf) { wcscpy(buf, text); sr->pOleStr = buf; }
    }
    void EnsureInner() {
        if (m_inner) return;
        CLSID clsCFS = {0x0E5AAE11,0xA475,0x4c5b,{0xAB,0x00,0xC6,0x6D,0xE4,0x00,0x27,0x4E}};
        CoCreateInstance(clsCFS, NULL, CLSCTX_INPROC_SERVER, IID_IShellFolder2, (void**)&m_inner);
    }
    LONG m_ref;
    IShellFolder2* m_inner;
    LPITEMIDLIST m_pidl;
    CLSID m_clsid;
    const wchar_t* m_name;
    const wchar_t* m_subtitle;
};

// ─── Class Factory ───

class ESClassFactory : public IClassFactory {
public:
    ESClassFactory(REFCLSID clsid) : m_ref(1), m_clsid(clsid) { InterlockedIncrement(&g_refCount); }
    ~ESClassFactory() { InterlockedDecrement(&g_refCount); }
    STDMETHODIMP QueryInterface(REFIID riid, void** ppv) {
        if (!ppv) return E_POINTER;
        if (IsEqualIID(riid, IID_IUnknown) || IsEqualIID(riid, IID_IClassFactory)) { *ppv = static_cast<IClassFactory*>(this); AddRef(); return S_OK; }
        *ppv = NULL; return E_NOINTERFACE;
    }
    STDMETHODIMP_(ULONG) AddRef() { return InterlockedIncrement(&m_ref); }
    STDMETHODIMP_(ULONG) Release() { LONG r = InterlockedDecrement(&m_ref); if (r == 0) delete this; return r; }
    STDMETHODIMP CreateInstance(IUnknown* pUnk, REFIID riid, void** ppv) {
        if (pUnk) return CLASS_E_NOAGGREGATION;
        EasyShareFolder* f = new EasyShareFolder(m_clsid);
        if (!f) return E_OUTOFMEMORY;
        HRESULT hr = f->QueryInterface(riid, ppv);
        f->Release();
        return hr;
    }
    STDMETHODIMP LockServer(BOOL lock) { if (lock) InterlockedIncrement(&g_refCount); else InterlockedDecrement(&g_refCount); return S_OK; }
private:
    LONG m_ref;
    CLSID m_clsid;
};

// ─── DLL Exports ───

extern "C" {
BOOL WINAPI DllMain(HINSTANCE h, DWORD reason, LPVOID) { if (reason == DLL_PROCESS_ATTACH) { g_hInst = h; DisableThreadLibraryCalls(h); } return TRUE; }
HRESULT WINAPI DllGetClassObject(REFCLSID rclsid, REFIID riid, void** ppv) {
    if (!IsEqualCLSID(rclsid, CLSID_ES_Cloud) && !IsEqualCLSID(rclsid, CLSID_ES_Share)) return CLASS_E_CLASSNOTAVAILABLE;
    ESClassFactory* cf = new ESClassFactory(rclsid);
    if (!cf) return E_OUTOFMEMORY;
    HRESULT hr = cf->QueryInterface(riid, ppv);
    cf->Release();
    return hr;
}
HRESULT WINAPI DllCanUnloadNow(void) { return (g_refCount == 0) ? S_OK : S_FALSE; }
}
