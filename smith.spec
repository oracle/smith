%global _dwz_low_mem_die_limit 0

Summary: Microcontainer Builder
Name:	 smith
Version: @VERSION@
Release: 3
License: None
Source0: %{name}-%{version}.tar.gz
BuildRequires: golang
Requires: mock, pigz


%description
Build microcontainers from rpms or oci images

%prep
%setup -q

%build
VERSION=%{version} make

%install
rm -rf %{buildroot}
%make_install

%clean
rm -rf %{buildroot}

%files
/usr/bin/smith

%changelog
* Thu Oct  6 2016 Vish Ishaya <vish.ishaya@oracle.com> - @VERSION@-3
- Remove mock files and add dependency on mock
* Wed Oct  5 2016 Vish Ishaya <vish.ishaya@oracle.com> - @VERSION@-2
- Remove mock files and add dependency on mock
* Wed Sep 28 2016 Vish Ishaya <vish.ishaya@oracle.com> - @VERSION@-1
- First smith build
